package nfc

import (
	"context"
	"time"
)

// Card is one detected NFC-A PICC.
type Card struct {
	UID        [7]byte
	UIDLen     uint8
	ATQA       uint16
	SAK        uint8
	TypeID     uint8
	UserMemory uint16
}

// TypeName returns the label for a Card.TypeID (same table as the official
// driver).
func TypeName(id uint8) string {
	switch id {
	case 1:
		return "MIFARE_Classic_Mini"
	case 2:
		return "MIFARE_Classic_1K"
	case 3:
		return "MIFARE_Classic_2K"
	case 4:
		return "MIFARE_Classic_4K"
	case 5:
		return "MIFARE_Ultralight/NTAG"
	case 6:
		return "MIFARE_Ultralight_EV1_1"
	case 7:
		return "MIFARE_Ultralight_EV1_2"
	case 8:
		return "MIFARE_Ultralight_Nano"
	case 9:
		return "MIFARE_UltralightC"
	case 10:
		return "NTAG_203"
	case 11:
		return "NTAG_210u"
	case 12:
		return "NTAG_210"
	case 13:
		return "NTAG_212"
	case 14:
		return "NTAG_213"
	case 15:
		return "NTAG_215"
	case 16:
		return "NTAG_216"
	case 17:
		return "ST25TA_512B"
	case 18:
		return "ST25TA_2K"
	case 19:
		return "ST25TA_16K"
	case 20:
		return "ST25TA_64K"
	case 21:
		return "ISO_14443_4"
	case 22:
		return "MIFARE_Plus_2K"
	case 23:
		return "MIFARE_Plus_4K"
	case 24:
		return "MIFARE_Plus_SE"
	case 25:
		return "MIFARE_DESFire_2K"
	case 26:
		return "MIFARE_DESFire_4K"
	case 27:
		return "MIFARE_DESFire_8K"
	case 28:
		return "MIFARE_DESFire_Light"
	case 29:
		return "NTAG_4XX"
	case 30:
		return "ISO_18092"
	}
	return "Unknown"
}

func identify(atqa uint16, sak uint8) (id uint8, memory uint16) {
	s := sak
	switch {
	case s&0x06 != 0:
		id = 0
	case s&0x08 != 0:
		if s&0x10 != 0 {
			if s&0x01 != 0 {
				id = 3
			} else {
				id = 4
			}
		} else if s&0x01 != 0 {
			id = 1
		} else {
			id = 2
		}
	case s&0x10 != 0:
		if s&0x01 != 0 {
			id = 23
		} else {
			id = 22
		}
	case s&0x01 != 0:
		id = 0
	case s&0x20 != 0:
		id = 21
	default:
		id = 5
	}
	switch id {
	case 1:
		memory = 240
	case 2:
		memory = 752
	case 3:
		memory = 1520
	case 4:
		memory = 3440
	case 5:
		memory = 48
	case 6, 7:
		memory = 48
	case 8:
		memory = 40
	case 9:
		memory = 144
	case 10:
		memory = 144
	case 11:
		memory = 48
	case 12:
		memory = 144
	case 13:
		memory = 208
	case 14:
		memory = 144
	case 15:
		memory = 504
	case 16:
		memory = 888
	case 17:
		memory = 64
	case 18:
		memory = 256
	case 19:
		memory = 2048
	case 20:
		memory = 8192
	case 22:
		memory = 1520
	case 23:
		memory = 3440
	case 24:
		memory = 752
	case 25:
		memory = 2048
	case 26:
		memory = 4096
	case 27:
		memory = 8192
	case 28:
		memory = 256
	}
	return id, memory
}

// Detect tries to activate one NFC-A PICC. It returns ok=false when no card
// answers (not an error).
func (d *Device) Detect(ctx context.Context) (Card, bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return Card{}, false, ErrNotBegun
	}
	if d.mode != modeA {
		return Card{}, false, ErrWrongMode
	}

	uid, uidLen, atqa, sak, ok, err := d.readUID(ctx)
	if err != nil {
		return Card{}, false, err
	}
	if !ok {
		return Card{}, false, nil
	}
	c := Card{UIDLen: uint8(uidLen), ATQA: atqa, SAK: sak}
	copy(c.UID[:], uid[:uidLen])
	c.TypeID, c.UserMemory = identify(atqa, sak)
	return c, true, nil
}

func (d *Device) readUID(ctx context.Context) (uid [7]byte, uidLen int, atqa uint16, sak uint8, ok bool, err error) {
	atqa, ok, err = d.requestWakeup(ctx, true)
	if err != nil {
		return
	}
	if !ok {
		atqa, ok, err = d.requestWakeup(ctx, false)
		if err != nil || !ok {
			return
		}
	}

	r1, ok, err := d.antiCollision(ctx, 0x93)
	if err != nil || !ok {
		return
	}
	cl1 := [4]byte{r1[0], r1[1], r1[2], r1[3]}
	sak, ok, err = d.selectCard(ctx, 0x93, cl1, r1[4])
	if err != nil || !ok {
		return
	}
	if cl1[0] != 0x88 {
		uid[0], uid[1], uid[2], uid[3] = cl1[0], cl1[1], cl1[2], cl1[3]
		uidLen = 4
		ok = true
		return
	}
	if sak&0x04 == 0 {
		return uid, 0, 0, 0, false, nil
	}

	r2, ok, err := d.antiCollision(ctx, 0x95)
	if err != nil || !ok {
		return
	}
	cl2 := [4]byte{r2[0], r2[1], r2[2], r2[3]}
	sak, ok, err = d.selectCard(ctx, 0x95, cl2, r2[4])
	if err != nil || !ok {
		return
	}
	if cl2[0] == 0x88 {
		return uid, 0, 0, 0, false, nil
	}
	uid[0], uid[1], uid[2], uid[3] = cl1[1], cl1[2], cl1[3], cl2[0]
	uid[4], uid[5], uid[6] = cl2[1], cl2[2], cl2[3]
	uidLen = 7
	ok = true
	return
}

func (d *Device) requestWakeup(ctx context.Context, reqa bool) (uint16, bool, error) {
	if err := d.writeFWT(timoREQA); err != nil {
		return 0, false, err
	}
	if err := d.writeReg(regISO14443ASettings, antcl); err != nil {
		return 0, false, err
	}
	if err := d.setBits(regAuxiliaryDefinition, noCRCRx); err != nil {
		return 0, false, err
	}
	if err := d.clearInterrupts(); err != nil {
		return 0, false, err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return 0, false, err
	}
	cmd := uint8(cmdTransmitREQA)
	if !reqa {
		cmd = cmdTransmitWUPA
	}
	if err := d.command(cmd, nil); err != nil {
		return 0, false, err
	}
	n, err := d.waitFIFO(ctx, 2, timoREQA)
	if err != nil {
		return 0, false, err
	}
	if n < 2 {
		return 0, false, nil
	}
	rb, err := d.readFIFO(n)
	if err != nil {
		return 0, false, err
	}
	atqa := uint16(rb[1])<<8 | uint16(rb[0])
	if atqa == 0 || atqa == 0xFFFF {
		return 0, false, nil
	}
	return atqa, true, nil
}

func (d *Device) antiCollision(ctx context.Context, sel uint8) ([5]byte, bool, error) {
	var out [5]byte
	if err := d.writeFWT(timoAntiColl); err != nil {
		return out, false, err
	}
	if err := d.writeReg(regISO14443ASettings, antcl); err != nil {
		return out, false, err
	}
	if err := d.clearBits(regAuxiliaryDefinition, noCRCRx); err != nil {
		return out, false, err
	}
	if err := d.clearInterrupts(); err != nil {
		return out, false, err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return out, false, err
	}
	if err := d.writeFIFO([]byte{sel, 0x20}); err != nil {
		return out, false, err
	}
	if err := d.writeNumTX(2, 0); err != nil {
		return out, false, err
	}
	if err := d.command(cmdTransmitWithoutCRC, nil); err != nil {
		return out, false, err
	}
	n, err := d.waitFIFO(ctx, 5, timoAntiColl)
	if err != nil {
		return out, false, err
	}
	if n < 5 {
		return out, false, nil
	}
	rb, err := d.readFIFO(n)
	if err != nil {
		return out, false, err
	}
	copy(out[:], rb[:5])
	if out[0]^out[1]^out[2]^out[3] != out[4] {
		return out, false, nil
	}
	return out, true, nil
}

func (d *Device) selectCard(ctx context.Context, sel uint8, uid4 [4]byte, bcc uint8) (uint8, bool, error) {
	frame := [7]byte{sel, 0x70, uid4[0], uid4[1], uid4[2], uid4[3], bcc}
	if err := d.writeFWT(timoSelect); err != nil {
		return 0, false, err
	}
	if err := d.writeReg(regISO14443ASettings, 0x00); err != nil {
		return 0, false, err
	}
	if err := d.clearBits(regAuxiliaryDefinition, noCRCRx); err != nil {
		return 0, false, err
	}
	if err := d.clearInterrupts(); err != nil {
		return 0, false, err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return 0, false, err
	}
	if err := d.writeFIFO(frame[:]); err != nil {
		return 0, false, err
	}
	if err := d.writeNumTX(len(frame), 0); err != nil {
		return 0, false, err
	}
	if err := d.command(cmdTransmitWithCRC, nil); err != nil {
		return 0, false, err
	}
	n, err := d.waitFIFO(ctx, 1, timoSelect)
	if err != nil {
		return 0, false, err
	}
	if n < 1 {
		return 0, false, nil
	}
	rb, err := d.readFIFO(n)
	if err != nil {
		return 0, false, err
	}
	return rb[0], true, nil
}

// Halt sends HLTA, putting the selected PICC into HALT state.
func (d *Device) Halt(ctx context.Context) error {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return ErrNotBegun
	}
	if err := d.writeFWT(10); err != nil {
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
	if err := d.writeFIFO([]byte{0x50, 0x00}); err != nil {
		return err
	}
	if err := d.writeNumTX(2, 0); err != nil {
		return err
	}
	if err := d.command(cmdTransmitWithCRC, nil); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	return nil
}

// FeliCaCard is one detected NFC-F PICC (FeliCa IDm and PMm).
type FeliCaCard struct {
	IDm [8]byte
	PMm [8]byte
}

// DetectF polls for one NFC-F card (212 kbps, wildcard system code).
func (d *Device) DetectF(ctx context.Context) (FeliCaCard, bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return FeliCaCard{}, false, ErrNotBegun
	}
	if d.mode != modeF {
		return FeliCaCard{}, false, ErrWrongMode
	}

	if err := d.writeFWT(timoPollingF); err != nil {
		return FeliCaCard{}, false, err
	}
	if err := d.clearBits(regAuxiliaryDefinition, noCRCRx); err != nil {
		return FeliCaCard{}, false, err
	}
	if err := d.clearInterrupts(); err != nil {
		return FeliCaCard{}, false, err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return FeliCaCard{}, false, err
	}
	frame := [5]byte{0x00, 0xFF, 0xFF, 0x00, 0x00} // Polling, wildcard, no request, slot 1
	if err := d.writeFIFO(frame[:]); err != nil {
		return FeliCaCard{}, false, err
	}
	if err := d.writeNumTX(len(frame), 0); err != nil {
		return FeliCaCard{}, false, err
	}
	if err := d.command(cmdTransmitWithCRC, nil); err != nil {
		return FeliCaCard{}, false, err
	}
	n, err := d.waitFIFO(ctx, 18, timoPollingF)
	if err != nil {
		return FeliCaCard{}, false, err
	}
	if n < 18 {
		return FeliCaCard{}, false, nil
	}
	rb, err := d.readFIFO(n)
	if err != nil {
		return FeliCaCard{}, false, err
	}
	if rb[0] < 18 || rb[1] != 0x01 { // length, Polling response
		return FeliCaCard{}, false, nil
	}
	var c FeliCaCard
	copy(c.IDm[:], rb[2:10])
	copy(c.PMm[:], rb[10:18])
	return c, true, nil
}

// NFCBCard is one detected NFC-B PICC.
type NFCBCard struct {
	PUPI        [4]byte
	Application [4]byte
	Protocol    [3]byte
}

// DetectB sends REQB (slot 1, AFI 0) and returns the ATQB fields.
func (d *Device) DetectB(ctx context.Context) (NFCBCard, bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return NFCBCard{}, false, ErrNotBegun
	}
	if d.mode != modeB {
		return NFCBCard{}, false, ErrWrongMode
	}
	if err := d.writeFWT(10); err != nil {
		return NFCBCard{}, false, err
	}
	if err := d.clearBits(regAuxiliaryDefinition, noCRCRx); err != nil {
		return NFCBCard{}, false, err
	}
	if err := d.clearInterrupts(); err != nil {
		return NFCBCard{}, false, err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return NFCBCard{}, false, err
	}
	frame := [3]byte{0x05, 0x00, 0x00} // REQB, AFI 0, slot 1
	if err := d.writeFIFO(frame[:]); err != nil {
		return NFCBCard{}, false, err
	}
	if err := d.writeNumTX(len(frame), 0); err != nil {
		return NFCBCard{}, false, err
	}
	if err := d.command(cmdTransmitWithCRC, nil); err != nil {
		return NFCBCard{}, false, err
	}
	n, err := d.waitFIFO(ctx, 12, 10)
	if err != nil {
		return NFCBCard{}, false, err
	}
	if n < 12 {
		return NFCBCard{}, false, nil
	}
	rb, err := d.readFIFO(n)
	if err != nil {
		return NFCBCard{}, false, err
	}
	if rb[0] != 0x50 { // ATQB
		return NFCBCard{}, false, nil
	}
	var c NFCBCard
	copy(c.PUPI[:], rb[1:5])
	copy(c.Application[:], rb[5:9])
	copy(c.Protocol[:], rb[9:12])
	return c, true, nil
}

// IsClassic reports whether the card is a MIFARE Classic family card.
func (c Card) IsClassic() bool { return c.TypeID >= 1 && c.TypeID <= 4 }

// IsType2 reports whether the card is an Ultralight/NTAG/ST25TA (Type 2 family).
func (c Card) IsType2() bool { return c.TypeID >= 5 && c.TypeID <= 19 }
