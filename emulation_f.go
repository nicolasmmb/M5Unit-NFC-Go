package nfc

import (
	"context"
	"time"
)

const (
	opLoadPTMemoryF = 0xA8
	cmdUnmaskRxData = 0xD1
	modeTargetFE    = 0xE0 // target, FeliCa bit-rate detection
	enBit           = 0x80
	rxEnBit         = 0x40
	txEnBit         = 0x08
	d2124241r       = 0x04
	noTxParBit      = 0x80
	noRxParBit      = 0x40
	nfcF0Bit        = 0x20
)

// EmulateFConfig selects the identity returned to a reader.
type EmulateFConfig struct {
	IDm [8]byte
	PMm [8]byte
	// SystemCode is advertised in the polling response (e.g. 0x88B4 for Lite).
	SystemCode uint16
}

// StartEmulationF loads the NFC-F PT memory and puts the ST25R3916 in passive
// target mode. The chip answers a wildcard Polling (SENSF) autonomously with
// IDm/PMm. Only identification is emulated; read/write commands are not.
func (d *Device) StartEmulationF(ctx context.Context, cfg EmulateFConfig) error {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return ErrNotBegun
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.SystemCode == 0 {
		cfg.SystemCode = fcSysLite
	}

	if err := d.command(cmdStopAllActivities, nil); err != nil {
		return err
	}
	// PT memory is only reachable in Ready mode (oscillator/regulator on).
	if err := d.setBits(regOperationControl, enBit); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	if aux, err := d.readReg(regAuxiliaryDisplay); err != nil {
		return err
	} else if aux&oscOK == 0 {
		return ErrOscillator
	}

	// PT memory F: system code, polling response code, M (IDm+PMm), request data.
	d.tx[0] = opLoadPTMemoryF
	d.tx[1] = byte(cfg.SystemCode >> 8)
	d.tx[2] = byte(cfg.SystemCode)
	d.tx[3] = 0x01 // Polling response
	copy(d.tx[4:12], cfg.IDm[:])
	copy(d.tx[12:20], cfg.PMm[:])
	d.tx[20] = 0x00
	d.tx[21] = 0x83 // auto detect, 424/212
	if err := d.bus.Tx(uint16(d.addr), d.tx[:22], nil); err != nil {
		return err
	}

	if err := d.writeReg(regModeDefinition, modeTargetFE); err != nil {
		return err
	}
	// 0x5C with d_212_424_1r cleared: auto response for NFC-F.
	if err := d.writeReg(regNFCIP1PassiveTarget, 0x58); err != nil {
		return err
	}
	if iso, err := d.readReg(regISO14443ASettings); err != nil {
		return err
	} else if err := d.writeReg(regISO14443ASettings, iso&^(noTxParBit|noRxParBit|nfcF0Bit)); err != nil {
		return err
	}
	if err := d.setBits(regAuxiliaryDefinition, noCRCRx); err != nil {
		return err
	}
	if err := d.writeReg(regAntennaTuning1, 0x00); err != nil {
		return err
	}
	if err := d.writeReg(regAntennaTuning2, 0xFF); err != nil {
		return err
	}
	// Unmask interrupts so activity can be observed; clear any pending.
	if err := d.write32(regMaskMainInterrupt, 0); err != nil {
		return err
	}
	if err := d.clearInterrupts(); err != nil {
		return err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return err
	}
	if err := d.command(cmdUnmaskRxData, nil); err != nil {
		return err
	}
	// Target mode: receiver on, transmitter off.
	if err := d.setBits(regOperationControl, enBit|rxEnBit); err != nil {
		return err
	}
	d.mode = modeNone
	d.emulating = true
	return nil
}

// StopEmulation leaves passive target mode.
func (d *Device) StopEmulation(ctx context.Context) error {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.emulating = false
	if err := d.command(cmdStopAllActivities, nil); err != nil {
		return err
	}
	return d.clearBits(regOperationControl, enBit|txEnBit|rxEnBit)
}

// EmulationActivity reports (and clears) any pending RF interrupt, so a UI can
// count reader polls/field events.
func (d *Device) EmulationActivity() (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.emulating {
		return false, nil
	}
	if err := d.readRegs(regMainInterrupt, d.rx[:4]); err != nil {
		return false, err
	}
	active := d.rx[0]|d.rx[1]|d.rx[2]|d.rx[3] != 0
	if active {
		// Reading the interrupt registers clears them.
		_ = d.clearInterrupts()
	}
	return active, nil
}

// Emulating reports whether passive NFC-F emulation is active.
func (d *Device) Emulating() bool {
	d.mu.Lock()
	e := d.emulating
	d.mu.Unlock()
	return e
}
