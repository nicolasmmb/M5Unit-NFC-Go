package nfc

import (
	"context"
	"time"
)

const noTxPar = 0x80

func (d *Device) rand32() uint32 {
	x := d.rng
	if x == 0 {
		x = 0x12345678
	}
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	d.rng = x
	return x
}

// nfcaTransceiveCRC sends a plain ISO14443-A frame with CRC and waits for at
// least minRx bytes. It returns a slice into the device RX buffer.
func (d *Device) nfcaTransceiveCRC(ctx context.Context, tx []byte, timeoutMS, minRx int) ([]byte, bool, error) {
	if err := d.writeFWT(timeoutMS); err != nil {
		return nil, false, err
	}
	if err := d.writeReg(regISO14443ASettings, 0x00); err != nil {
		return nil, false, err
	}
	if err := d.clearBits(regAuxiliaryDefinition, noCRCRx); err != nil {
		return nil, false, err
	}
	if err := d.clearInterrupts(); err != nil {
		return nil, false, err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return nil, false, err
	}
	if err := d.writeFIFO(tx); err != nil {
		return nil, false, err
	}
	if err := d.writeNumTX(len(tx), 0); err != nil {
		return nil, false, err
	}
	if err := d.command(cmdTransmitWithCRC, nil); err != nil {
		return nil, false, err
	}
	n, err := d.waitFIFO(ctx, minRx, timeoutMS)
	if err != nil {
		return nil, false, err
	}
	if n < minRx {
		return nil, false, nil
	}
	// Let the last bytes arrive (stable size), like the official driver.
	time.Sleep(time.Millisecond)
	n2, err := d.fifoSize()
	if err != nil {
		return nil, false, err
	}
	if n2 > n {
		n = n2
	}
	rb, err := d.readFIFO(n)
	if err != nil {
		return nil, false, err
	}
	return rb, true, nil
}

// Type2ReadQuad reads 4 pages (16 bytes) starting at a 4-page boundary.
func (d *Device) Type2ReadQuad(ctx context.Context, basePage uint8) ([16]byte, bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.mode != modeA && d.begun {
		return [16]byte{}, false, ErrWrongMode
	}
	if !d.begun {
		return [16]byte{}, false, ErrNotBegun
	}
	return d.type2ReadQuadLocked(ctx, basePage)
}

func (d *Device) type2ReadQuadLocked(ctx context.Context, basePage uint8) ([16]byte, bool, error) {
	var out [16]byte
	rb, ok, err := d.nfcaTransceiveCRC(ctx, []byte{0x30, basePage & 0xFC}, 24, 18)
	if err != nil || !ok || len(rb) < 18 {
		return out, false, err
	}
	copy(out[:], rb[:16])
	return out, true, nil
}

// Type2ReadPage reads one page (4 bytes) of an Ultralight/NTAG card. The card
// must have been activated by Detect and not halted.
func (d *Device) Type2ReadPage(ctx context.Context, page uint8) ([4]byte, bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return [4]byte{}, false, ErrNotBegun
	}
	if d.mode != modeA {
		return [4]byte{}, false, ErrWrongMode
	}
	return d.type2ReadPageLocked(ctx, page)
}

func (d *Device) type2ReadPageLocked(ctx context.Context, page uint8) ([4]byte, bool, error) {
	var out [4]byte
	q, ok, err := d.type2ReadQuadLocked(ctx, page)
	if err != nil || !ok {
		return out, false, err
	}
	off := int(page&0x03) * 4
	copy(out[:], q[off:off+4])
	return out, true, nil
}

// Type2WritePage writes one page and verifies it by reading it back.
// Only user pages should be written (never UID/lock/config pages).
func (d *Device) Type2WritePage(ctx context.Context, page uint8, data [4]byte) (bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return false, ErrNotBegun
	}
	if d.mode != modeA {
		return false, ErrWrongMode
	}
	frame := [6]byte{0xA2, page, data[0], data[1], data[2], data[3]}
	if err := d.nfcaTransceiveCRCAck(ctx, frame[:], 40); err != nil {
		return false, err
	}
	got, ok, err := d.type2ReadPageLocked(ctx, page)
	if err != nil || !ok {
		return false, err
	}
	return got == data, nil
}

// ClassicReadBlock authenticates with key A and reads one 16-byte block.
// The card must have been activated by Detect.
func (d *Device) ClassicReadBlock(ctx context.Context, uid []byte, block uint8, key [6]byte) ([16]byte, bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	var out [16]byte
	if !d.begun {
		return out, false, ErrNotBegun
	}
	if d.mode != modeA {
		return out, false, ErrWrongMode
	}
	ok, err := d.classicAuthLocked(ctx, uid, block, key)
	if err != nil || !ok {
		d.classicEndSessionLocked()
		return out, false, err
	}
	out, ok, err = d.classicReadLocked(ctx, block)
	d.classicEndSessionLocked()
	return out, ok, err
}

func (d *Device) classicAuthLocked(ctx context.Context, uid []byte, block uint8, key [6]byte) (bool, error) {
	d.authenticated = false
	cmd := uint8(0x60) // Key A
	rb, ok, err := d.nfcaTransceiveCRC(ctx, []byte{cmd, block}, 24, 4)
	if err != nil || !ok || len(rb) < 4 {
		return false, err
	}
	var rb4 [4]byte
	copy(rb4[:], rb[:4])
	time.Sleep(87 * time.Microsecond)

	u32 := uidTailU32BE(uid)
	nt := ntFromRB(rb4[:])
	var c Crypto1
	c.init(keyToU48(key))
	c.inject(u32, nt)
	nr := d.rand32()
	ar, suc3 := suc23(byteswap32(nt))
	ab, parity := c.encryptAB(nr, ar)
	n := appendParityTo(d.parityBuf[:], ab[:], parity)

	if err := d.command(cmdResetRXGain, nil); err != nil {
		return false, err
	}
	if err := d.writeFWT(20); err != nil {
		return false, err
	}
	if err := d.writeReg(regISO14443ASettings, noTxPar); err != nil {
		return false, err
	}
	if err := d.setBits(regAuxiliaryDefinition, noCRCRx); err != nil {
		return false, err
	}
	if err := d.clearInterrupts(); err != nil {
		return false, err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return false, err
	}
	if err := d.writeFIFO(d.parityBuf[:n]); err != nil {
		return false, err
	}
	if err := d.writeNumTX(9, 0); err != nil { // 8 data bytes + parity = 72 bits
		return false, err
	}
	if err := d.command(cmdTransmitWithoutCRC, nil); err != nil {
		return false, err
	}
	got, err := d.waitFIFO(ctx, 4, 40)
	if err != nil {
		return false, err
	}
	if got < 4 {
		return false, nil
	}
	ba, err := d.readFIFO(4)
	if err != nil {
		return false, err
	}
	var at2 [4]byte
	for i := 0; i < 4; i++ {
		at2[i] = ba[i] ^ c.step8(0, false)
	}
	at32 := uint32(at2[0]) | uint32(at2[1])<<8 | uint32(at2[2])<<16 | uint32(at2[3])<<24
	if at32 != suc3 {
		return false, nil
	}
	d.c1 = c
	d.authenticated = true
	if err := d.writeReg(regISO14443ASettings, 0x00); err != nil {
		return false, err
	}
	return true, d.clearBits(regAuxiliaryDefinition, noCRCRx)
}

func (d *Device) classicReadLocked(ctx context.Context, block uint8) ([16]byte, bool, error) {
	var out [16]byte
	if !d.authenticated {
		return out, false, nil
	}
	var plain [4]byte
	plain[0] = 0x30
	plain[1] = block
	crc := crc16A(plain[:2])
	plain[2] = byte(crc)
	plain[3] = byte(crc >> 8)
	enc, parity := d.c1.encryptStream(plain[:])
	n := appendParityTo(d.parityBuf[:], enc, parity)
	if err := d.writeFWT(48); err != nil {
		return out, false, err
	}
	if err := d.writeReg(regISO14443ASettings, noTxPar); err != nil {
		return out, false, err
	}
	if err := d.setBits(regAuxiliaryDefinition, noCRCRx); err != nil {
		return out, false, err
	}
	if err := d.clearInterrupts(); err != nil {
		return out, false, err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return out, false, err
	}
	if err := d.writeFIFO(d.parityBuf[:n]); err != nil {
		return out, false, err
	}
	if err := d.writeNumTX(4, 4); err != nil { // 36 bits
		return out, false, err
	}
	if err := d.command(cmdTransmitWithoutCRC, nil); err != nil {
		return out, false, err
	}
	got, err := d.waitFIFO(ctx, 18, 48)
	if err != nil {
		return out, false, err
	}
	if got < 18 {
		return out, false, nil
	}
	rb, err := d.readFIFO(18)
	if err != nil {
		return out, false, err
	}
	for i := 0; i < 18; i++ {
		rb[i] ^= d.c1.step8(0, false)
	}
	check := crc16A(rb[:16])
	lo, hi := byte(check), byte(check>>8)
	if !((rb[16] == lo && rb[17] == hi) || (rb[16] == hi && rb[17] == lo)) {
		return out, false, nil
	}
	copy(out[:], rb[:16])
	return out, true, nil
}

// ClassicWriteBlock authenticates with key A, writes one 16-byte block and
// verifies it by reading it back. Never write block 0 (UID) or a sector
// trailer (block 3 of each sector).
func (d *Device) ClassicWriteBlock(ctx context.Context, uid []byte, block uint8, key [6]byte, data [16]byte) (bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return false, ErrNotBegun
	}
	if d.mode != modeA {
		return false, ErrWrongMode
	}
	ok, err := d.classicAuthLocked(ctx, uid, block, key)
	if err != nil || !ok {
		d.classicEndSessionLocked()
		return false, err
	}
	ok, err = d.classicWriteLocked(ctx, block, data)
	d.classicEndSessionLocked()
	if err != nil || !ok {
		return false, err
	}
	// Verify with a fresh authentication and read.
	ok, err = d.classicAuthLocked(ctx, uid, block, key)
	if err != nil || !ok {
		d.classicEndSessionLocked()
		return false, err
	}
	got, ok, err := d.classicReadLocked(ctx, block)
	d.classicEndSessionLocked()
	if err != nil || !ok {
		return false, err
	}
	return got == data, nil
}

func (d *Device) classicWriteLocked(ctx context.Context, block uint8, data [16]byte) (bool, error) {
	cmd := [2]byte{0xA0, block}
	ok, err := d.classicTransceiveEncryptLocked(ctx, cmd[:], 1, false, 12)
	if err != nil || !ok {
		return false, err
	}
	time.Sleep(82 * time.Microsecond)
	return d.classicTransceiveEncryptLocked(ctx, data[:], 1, false, 24)
}

// classicTransceiveEncryptLocked encrypts a plain frame (and CRC when
// includeCRC) with the current Crypto1 state and checks the ACK or payload.
func (d *Device) classicTransceiveEncryptLocked(ctx context.Context, plain []byte, rxPayloadLen int, includeCRC bool, timeoutMS int) (bool, error) {
	var tx [24]byte
	n := copy(tx[:], plain)
	if includeCRC {
		crc := crc16A(tx[:n])
		tx[n] = byte(crc)
		tx[n+1] = byte(crc >> 8)
		n += 2
	}
	enc, parity := d.c1.encryptStream(tx[:n])
	pn := appendParityTo(d.parityBuf[:], enc, parity)
	nbits := n * 9
	if err := d.writeReg(regISO14443ASettings, noTxPar); err != nil {
		return false, err
	}
	if includeCRC {
		if err := d.setBits(regAuxiliaryDefinition, noCRCRx); err != nil {
			return false, err
		}
	}
	if err := d.writeFWT(timeoutMS); err != nil {
		return false, err
	}
	if err := d.clearInterrupts(); err != nil {
		return false, err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return false, err
	}
	if err := d.writeFIFO(d.parityBuf[:pn]); err != nil {
		return false, err
	}
	if err := d.writeNumTX(nbits/8, nbits%8); err != nil {
		return false, err
	}
	if err := d.command(cmdTransmitWithoutCRC, nil); err != nil {
		return false, err
	}
	rlen := rxPayloadLen
	if includeCRC {
		rlen += 2
	}
	got, err := d.waitFIFO(ctx, rlen, timeoutMS)
	if err != nil || got < rlen {
		return false, err
	}
	rb, err := d.readFIFO(rlen)
	if err != nil {
		return false, err
	}
	if rlen == 1 {
		ret := rb[0] & 0x0F
		var res uint8
		for i := 0; i < 4; i++ {
			res |= (d.c1.stepWith(0, false) ^ ((ret >> uint(i)) & 1)) << uint(i)
		}
		return res == 0x0A, nil
	}
	for i := 0; i < rlen; i++ {
		rb[i] ^= d.c1.step8(0, false)
	}
	if includeCRC {
		crc := crc16A(rb[:rxPayloadLen])
		lo, hi := byte(crc), byte(crc>>8)
		if !((rb[rxPayloadLen] == lo && rb[rxPayloadLen+1] == hi) ||
			(rb[rxPayloadLen] == hi && rb[rxPayloadLen+1] == lo)) {
			return false, nil
		}
	}
	return true, nil
}

// nfcaTransceiveCRCAck sends a frame with CRC and checks the MIFARE ACK nibble.
func (d *Device) nfcaTransceiveCRCAck(ctx context.Context, tx []byte, timeoutMS int) error {
	if err := d.writeFWT(timeoutMS); err != nil {
		return err
	}
	if err := d.writeReg(regISO14443ASettings, 0x00); err != nil {
		return err
	}
	if err := d.clearBits(regAuxiliaryDefinition, noCRCRx); err != nil {
		return err
	}
	if err := d.clearInterrupts(); err != nil {
		return err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return err
	}
	if err := d.writeFIFO(tx); err != nil {
		return err
	}
	if err := d.writeNumTX(len(tx), 0); err != nil {
		return err
	}
	if err := d.command(cmdTransmitWithCRC, nil); err != nil {
		return err
	}
	n, err := d.waitFIFO(ctx, 1, timeoutMS)
	if err != nil {
		return err
	}
	if n < 1 {
		return ErrTimeout
	}
	time.Sleep(time.Millisecond)
	rb, err := d.readFIFO(n)
	if err != nil {
		return err
	}
	if rb[0]&0x0F != 0x0A {
		return ErrNotAck
	}
	return nil
}

func (d *Device) classicEndSessionLocked() {
	d.authenticated = false
	d.c1 = Crypto1{}
	_ = d.writeReg(regISO14443ASettings, 0x00)
	_ = d.clearBits(regAuxiliaryDefinition, noCRCRx)
}
