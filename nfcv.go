package nfc

import "context"

// ISO15693 request flags.
const (
	vcdHighRate      = 0x02
	vcdInventory     = 0x04
	vcdNbSlots       = 0x20
	vcdSof           = 0x21
	vcdEof           = 0x04
	vcdInventoryCmd  = 0x01
	vcdTimoInventory = 16
)

// ISO15693Card is one detected NFC-V (ISO15693) PICC.
type ISO15693Card struct {
	UID    [8]byte // manufacturer code first
	DSFID  uint8
	AFI    uint8
	HasAFI bool
}

// ConfigureV switches a begun device to NFC-V (ISO15693, subcarrier stream).
func (d *Device) ConfigureV(ctx context.Context) error {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return ErrNotBegun
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.command(cmdStopAllActivities, nil); err != nil {
		return err
	}
	for _, rv := range [...]struct{ reg, val uint8 }{
		{regReceiverConfig1, 0x13},
		{regReceiverConfig2, 0x2D},
		{regReceiverConfig3, 0x00},
		{regReceiverConfig4, 0x00},
		{regTXDriver, 0x70},
		{regIOConfig1, 0x07},
	} {
		if err := d.writeReg(rv.reg, rv.val); err != nil {
			return err
		}
	}
	if err := d.setBits(regIOConfig2, aatEn); err != nil {
		return err
	}
	if err := d.setBits(regOperationControl, 0x03); err != nil {
		return err
	}
	if err := d.writeReg(regModeDefinition, 0x38); err != nil { // stream mode definition
		return err
	}
	if err := d.writeReg(regAuxiliaryDefinition, 0x02); err != nil {
		return err
	}
	for _, rv := range [...]struct{ reg, val uint8 }{
		{0x05, 0x40}, // EMD suppression
		{0x06, 0x14}, // subcarrier start timer
		{0x0B, 0x0C}, // P2P receiver
		{regCorrelatorConfig1, 0x13},
		{regCorrelatorConfig2, 0x01},
		{0x0F, 0x00}, // squelch timer
		{0x15, 0x00}, // NFC field-on guard
		{0x28, 0x10}, // auxiliary modulation
		{0x29, 0x7C}, // TX driver timing
		{0x2A, 0x80}, // resistive AM modulation
	} {
		if err := d.writeRegSpaceB(rv.reg, rv.val); err != nil {
			return err
		}
	}
	if err := d.writeReg(regModeDefinition, 0x70); err != nil { // subcarrier stream
		return err
	}
	if err := d.fieldOn(); err != nil {
		return err
	}
	d.mode = modeV
	return nil
}

// DetectV sends an ISO15693 Inventory (single slot, high data rate) and
// returns the UID/DSFID of the first card.
func (d *Device) DetectV(ctx context.Context) (ISO15693Card, bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return ISO15693Card{}, false, ErrNotBegun
	}
	if d.mode != modeV {
		return ISO15693Card{}, false, ErrWrongMode
	}

	// Inventory frame: flags, command, mask length 0, CRC16.
	var frame [5]byte
	frame[0] = vcdInventory | vcdNbSlots | vcdHighRate // high rate, 1 slot
	frame[1] = vcdInventoryCmd
	frame[2] = 0x00
	crc := crc16ISO(frame[:3])
	frame[3] = byte(crc)
	frame[4] = byte(crc >> 8)

	var enc [32]byte
	n := encodeVCD(enc[:], frame[:])
	if err := d.writeFWT(vcdTimoInventory); err != nil {
		return ISO15693Card{}, false, err
	}
	if err := d.clearInterrupts(); err != nil {
		return ISO15693Card{}, false, err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return ISO15693Card{}, false, err
	}
	if err := d.writeFIFO(enc[:n]); err != nil {
		return ISO15693Card{}, false, err
	}
	if err := d.writeNumTX(n, 0); err != nil {
		return ISO15693Card{}, false, err
	}
	if err := d.command(cmdTransmitWithoutCRC, nil); err != nil {
		return ISO15693Card{}, false, err
	}
	got, err := d.waitFIFO(ctx, 10, vcdTimoInventory)
	if err != nil {
		return ISO15693Card{}, false, err
	}
	if got < 10 {
		return ISO15693Card{}, false, nil
	}
	rb, err := d.readFIFO(got)
	if err != nil {
		return ISO15693Card{}, false, err
	}
	var decoded [16]byte
	dn, ok := decodeVICC(decoded[:], rb, 0)
	if !ok || dn < 10 {
		return ISO15693Card{}, false, nil
	}
	if decoded[0] != 0x00 { // response flags
		return ISO15693Card{}, false, nil
	}
	var c ISO15693Card
	c.DSFID = decoded[1]
	for i := 0; i < 8; i++ {
		c.UID[i] = decoded[9-i]
	}
	return c, true, nil
}

// encodeVCD encodes an ISO15693 frame as 1-of-4 pulse position modulation.
func encodeVCD(dst, frame []byte) int {
	dst[0] = vcdSof
	n := 1
	for _, b := range frame {
		for i := 0; i < 4; i++ {
			switch b & 0x03 {
			case 0:
				dst[n] = 0x02
			case 1:
				dst[n] = 0x08
			case 2:
				dst[n] = 0x20
			case 3:
				dst[n] = 0x80
			}
			n++
			b >>= 2
		}
	}
	dst[n] = vcdEof
	return n + 1
}

// crc16ISO is the ISO15693 CRC-16 (reflected, init/xorout 0xFFFF).
func crc16ISO(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return crc ^ 0xFFFF
}

// decodeVICC decodes a Manchester-encoded ISO15693 response. Bits are
// LSB-first and the first five bits are the SOF.
func decodeVICC(dst, buf []byte, ignoreBits int) (int, bool) {
	if len(buf) == 0 || buf[0]&0x1F != 0x17 {
		return 0, false
	}
	manBits := len(buf) * 8
	maxPayloadBits := 0
	if manBits > 5 {
		maxPayloadBits = (manBits - 5) / 2
	}
	outLen := (maxPayloadBits + 7) / 8
	if outLen == 0 {
		return 0, false
	}
	if outLen > len(dst) {
		outLen = len(dst)
	}
	for i := 0; i < outLen; i++ {
		dst[i] = 0
	}
	mp, bp := 5, 0
	for ; mp < manBits-2; mp += 2 {
		man := (buf[mp/8] >> (mp % 8)) & 1
		man |= ((buf[(mp+1)/8] >> ((mp + 1) % 8)) & 1) << 1
		switch man {
		case 1:
			bp++
		case 2:
			if bp/8 < outLen {
				dst[bp/8] |= 1 << (bp % 8)
			}
			bp++
		default:
			if bp >= ignoreBits {
				return 0, false
			}
			bp++
		}
		if bp%8 == 0 {
			bytePos := mp / 8
			if bytePos+1 < len(buf) && buf[bytePos]&0xE0 == 0xA0 && buf[bytePos+1] == 0x03 {
				return bp / 8, true
			}
		}
		if bp >= outLen*8 {
			return bp / 8, true
		}
	}
	return bp / 8, true
}
