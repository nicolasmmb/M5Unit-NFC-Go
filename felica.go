package nfc

import "context"

// FeliCa types.
const (
	FeliCaUnknown  uint8 = 0
	FeliCaStandard uint8 = 1
	FeliCaLite     uint8 = 2
	FeliCaPlug     uint8 = 3
)

// System codes used by the official driver.
const (
	fcSysNDEF   = 0x12FC
	fcSysShared = 0xFE00
	fcSysLite   = 0x88B4
	fcSysPlug   = 0xFEE1
)

// FeliCaInfo is a detected FeliCa card with its system codes and resolved type.
type FeliCaInfo struct {
	IDm         [8]byte
	PMm         [8]byte
	SystemCodes [16]uint16
	SystemCount uint8
	Type        uint8
}

// TypeName returns a generic FeliCa type label.
func (i FeliCaInfo) TypeName() string {
	switch i.Type {
	case FeliCaStandard:
		return "FeliCa Standard"
	case FeliCaLite:
		return "FeliCa Lite/Lite-S"
	case FeliCaPlug:
		return "FeliCa Plug"
	}
	return "FeliCa"
}

func (d *Device) feliCaTransceive(ctx context.Context, frame []byte, minRx, timeoutMS int) ([]byte, bool, error) {
	if err := d.writeFWT(timeoutMS); err != nil {
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
	if err := d.writeFIFO(frame); err != nil {
		return nil, false, err
	}
	if err := d.writeNumTX(len(frame), 0); err != nil {
		return nil, false, err
	}
	if err := d.command(cmdTransmitWithCRC, nil); err != nil {
		return nil, false, err
	}
	n, err := d.waitFIFO(ctx, minRx, timeoutMS)
	if err != nil || n < minRx {
		return nil, false, err
	}
	rb, err := d.readFIFO(n)
	if err != nil {
		return nil, false, err
	}
	return rb, true, nil
}

// DetectFInfo polls a FeliCa card and requests its system codes, resolving a
// generic type (Standard/Lite/Plug).
func (d *Device) DetectFInfo(ctx context.Context) (FeliCaInfo, bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	var info FeliCaInfo
	if !d.begun {
		return info, false, ErrNotBegun
	}
	if d.mode != modeF {
		return info, false, ErrWrongMode
	}

	poll := [5]byte{0x00, 0xFF, 0xFF, 0x00, 0x00}
	rb, ok, err := d.feliCaTransceive(ctx, poll[:], 18, timoPollingF)
	if err != nil || !ok {
		return info, false, err
	}
	if rb[0] < 18 || rb[1] != 0x01 {
		return info, false, nil
	}
	copy(info.IDm[:], rb[2:10])
	copy(info.PMm[:], rb[10:18])

	// Request System Code (0x0C): card returns its system code list.
	req := [9]byte{0x0C}
	copy(req[1:], info.IDm[:])
	rb, ok, err = d.feliCaTransceive(ctx, req[:], 11, 12)
	if err != nil {
		return info, true, err
	}
	if !ok || rb[0] < 11 || rb[1] != 0x0D {
		info.Type = FeliCaUnknown
		return info, true, nil
	}
	count := int(rb[10])
	if count > len(info.SystemCodes) {
		count = len(info.SystemCodes)
	}
	info.SystemCount = uint8(count)
	hasLite, hasPlug := false, false
	for i := 0; i < count; i++ {
		code := uint16(rb[11+2*i])<<8 | uint16(rb[12+2*i])
		info.SystemCodes[i] = code
		switch code {
		case fcSysLite:
			hasLite = true
		case fcSysPlug:
			hasPlug = true
		}
	}
	switch {
	case hasPlug:
		info.Type = FeliCaPlug
	case hasLite:
		info.Type = FeliCaLite
	case count > 0:
		info.Type = FeliCaStandard
	}
	return info, true, nil
}

// FeliCaReadBlock reads one 16-byte block from a FeliCa service without
// encryption. The service/block pair must belong to a readable area.
func (d *Device) FeliCaReadBlock(ctx context.Context, info FeliCaInfo, service, block uint16) ([16]byte, bool, error) {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	var out [16]byte
	if !d.begun {
		return out, false, ErrNotBegun
	}
	if d.mode != modeF {
		return out, false, ErrWrongMode
	}
	req := [15]byte{0x06}
	copy(req[1:], info.IDm[:])
	req[9] = 0x01
	req[10] = byte(service)
	req[11] = byte(service >> 8)
	req[12] = 0x01
	req[13] = byte(block)
	req[14] = byte(block >> 8)
	rb, ok, err := d.feliCaTransceive(ctx, req[:], 29, 50)
	if err != nil || !ok {
		return out, false, err
	}
	if rb[0] < 29 || rb[1] != 0x07 || rb[10] != 0x00 || rb[11] != 0x00 {
		return out, false, nil
	}
	copy(out[:], rb[13:29])
	return out, true, nil
}
