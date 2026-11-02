// Package nfc drives the M5Stack Unit NFC (SKU U216), built around the
// ST25R3916 NFC front end on I2C (address 0x50).
//
// The driver is pure Go, uses fixed buffers and performs no heap allocation
// per operation. This file implements the ST25R3916 transport, initialization
// and the NFC-A helpers used by the probe.
package nfc

import (
	"context"
	"errors"
	"sync"
	"time"
)

// I2C is the subset of tinygo.org/x/drivers.I2C used by the unit.
type I2C interface {
	Tx(addr uint16, w, r []byte) error
}

const (
	// DefaultAddress is the 7-bit I2C address of the Unit NFC.
	DefaultAddress = 0x50

	opRead        = 0x40
	opWrite       = 0x00
	cmdSpaceB     = 0xFB
	opLoadFIFO    = 0x80
	opReadFIFO    = 0x9F
	defaultPoll   = time.Millisecond
	defaultCmdGap = 2 * time.Millisecond
)

// Commands.
const (
	cmdSetDefault         = 0xC1
	cmdStopAllActivities  = 0xC2
	cmdTransmitWithCRC    = 0xC4
	cmdTransmitWithoutCRC = 0xC5
	cmdTransmitREQA       = 0xC6
	cmdTransmitWUPA       = 0xC7
	cmdNFCInitialFieldOn  = 0xC8
	cmdResetRXGain        = 0xD5
	cmdAdjustRegulators   = 0xD6
	cmdClearFIFO          = 0xDB
	cmdTestAccess         = 0xFC
)

// Registers.
const (
	regIOConfig1             = 0x00
	regIOConfig2             = 0x01
	regOperationControl      = 0x02
	regModeDefinition        = 0x03
	regBitrateDefinition     = 0x04
	regISO14443ASettings     = 0x05
	regNFCIP1PassiveTarget   = 0x08
	regAuxiliaryDefinition   = 0x0A
	regReceiverConfig1       = 0x0B
	regReceiverConfig2       = 0x0C
	regReceiverConfig3       = 0x0D
	regReceiverConfig4       = 0x0E
	regNoResponseTimer1      = 0x10
	regTimerEMVControl       = 0x12
	regMaskMainInterrupt     = 0x16
	regMainInterrupt         = 0x1A
	regFIFOStatus1           = 0x1E
	regNumTransmittedBytes1  = 0x22
	regAntennaTuning1        = 0x26
	regAntennaTuning2        = 0x27
	regTXDriver              = 0x28
	regPassiveTargetMod      = 0x29
	regFieldDetectActivation = 0x2A
	regFieldDetectDeactiv    = 0x2B
	regAuxiliaryDisplay      = 0x31
	regICIdentity            = 0x3F
)

// Space-B registers (16-bit address space).
const (
	regSpaceBEMDSuppression = 0x05
	regSpaceBResistiveAM    = 0x2A
	regCorrelatorConfig1    = 0x0C
	regCorrelatorConfig2    = 0x0D
	regOvershootConfig1     = 0x30
	regOvershootConfig2     = 0x31
	regUndershootConfig1    = 0x32
	regUndershootConfig2    = 0x33
)

// Bit values.
const (
	validIdentifyType = 0x05
	opEn              = 0x80
	wuBit             = 0x04
	iOsc              = 0x80
	oscOK             = 0x10
	aatEn             = 0x20
	maskPreAdjust     = 0xFFFF00FF

	txEn    = 0x08
	rxEn    = 0x40
	noCRCRx = 0x80
	antcl   = 0x01
	disCorr = 0x04

	rxcfg1Z600K = 0x08
	rxcfg2      = 0x2D
	rxcfg3      = 0xD8
	rxcfg4      = 0x22

	timoREQA     = 12
	timoAntiColl = 8
	timoSelect   = 8
	timoPollingF = 12

	modeNone = 0
	modeA    = 1
	modeF    = 2
	modeB    = 3
	modeV    = 4

	fcHz = 13560000
)

// Errors returned by the driver.
var (
	ErrNoChip     = errors.New("nfc: ST25R3916 not found or unsupported identity")
	ErrOscillator = errors.New("nfc: oscillator did not start")
	ErrNotBegun   = errors.New("nfc: Begin must be called first")
	ErrWrongMode  = errors.New("nfc: protocol mode does not match")
	ErrTimeout    = errors.New("nfc: timeout")
	ErrNotAck     = errors.New("nfc: card did not acknowledge")
)

// Device is a Unit NFC reader. It is safe for concurrent use.
type Device struct {
	mu sync.Mutex

	bus  I2C
	addr uint8

	pollDelay time.Duration
	cmdDelay  time.Duration

	tx [32]byte
	rx [64]byte

	begun bool
	mode  uint8

	c1            Crypto1
	authenticated bool
	rng           uint32
	parityBuf     [16]byte
}

// New returns a Device on bus with the default I2C address.
func New(bus I2C) *Device {
	return &Device{
		bus:       bus,
		addr:      DefaultAddress,
		pollDelay: defaultPoll,
		cmdDelay:  defaultCmdGap,
		rng:       0x9E3779B9 ^ uint32(time.Now().UnixNano()),
	}
}

// Address returns the configured 7-bit I2C address.
func (d *Device) Address() uint8 { return d.addr }

// --- transport ---

func (d *Device) readReg(reg uint8) (uint8, error) {
	d.tx[0] = reg | opRead
	if err := d.bus.Tx(uint16(d.addr), d.tx[:1], d.rx[:1]); err != nil {
		return 0, err
	}
	return d.rx[0], nil
}

func (d *Device) writeReg(reg, val uint8) error {
	d.tx[0] = reg
	d.tx[1] = val
	return d.bus.Tx(uint16(d.addr), d.tx[:2], nil)
}

func (d *Device) readRegSpaceB(reg uint8) (uint8, error) {
	d.tx[0] = cmdSpaceB
	d.tx[1] = (reg & 0x3F) | opRead
	if err := d.bus.Tx(uint16(d.addr), d.tx[:2], d.rx[:1]); err != nil {
		return 0, err
	}
	return d.rx[0], nil
}

func (d *Device) writeRegSpaceB(reg, val uint8) error {
	d.tx[0] = cmdSpaceB
	d.tx[1] = reg & 0x3F
	d.tx[2] = val
	if err := d.bus.Tx(uint16(d.addr), d.tx[:3], nil); err != nil {
		return err
	}
	time.Sleep(50 * time.Microsecond)
	return nil
}

func (d *Device) command(cmd uint8, payload []byte) error {
	d.tx[0] = cmd
	copy(d.tx[1:], payload)
	if err := d.bus.Tx(uint16(d.addr), d.tx[:1+len(payload)], nil); err != nil {
		return err
	}
	if d.cmdDelay > 0 {
		time.Sleep(d.cmdDelay)
	}
	return nil
}

func (d *Device) read16(reg uint8) (uint16, error) {
	hi, err := d.readReg(reg)
	if err != nil {
		return 0, err
	}
	lo, err := d.readReg(reg + 1)
	if err != nil {
		return 0, err
	}
	return uint16(hi)<<8 | uint16(lo), nil
}

func (d *Device) write16(reg uint8, v uint16) error {
	if err := d.writeReg(reg, uint8(v>>8)); err != nil {
		return err
	}
	return d.writeReg(reg+1, uint8(v))
}

func (d *Device) write32(reg uint8, v uint32) error {
	for i := 0; i < 4; i++ {
		if err := d.writeReg(reg+uint8(i), uint8(v>>(24-8*i))); err != nil {
			return err
		}
	}
	return nil
}

func (d *Device) setBits(reg, bits uint8) error {
	v, err := d.readReg(reg)
	if err != nil {
		return err
	}
	return d.writeReg(reg, v|bits)
}

func (d *Device) clearBits(reg, bits uint8) error {
	v, err := d.readReg(reg)
	if err != nil {
		return err
	}
	return d.writeReg(reg, v&^bits)
}

func (d *Device) clearInterrupts() error {
	for i := 0; i < 4; i++ {
		if _, err := d.readReg(regMainInterrupt + uint8(i)); err != nil {
			return err
		}
	}
	return nil
}

func (d *Device) writeFIFO(p []byte) error {
	d.tx[0] = opLoadFIFO
	copy(d.tx[1:], p)
	if err := d.bus.Tx(uint16(d.addr), d.tx[:1+len(p)], nil); err != nil {
		return err
	}
	time.Sleep(50 * time.Microsecond)
	return nil
}

func (d *Device) readFIFO(n int) ([]byte, error) {
	if n > len(d.rx) {
		n = len(d.rx)
	}
	d.tx[0] = opReadFIFO
	if err := d.bus.Tx(uint16(d.addr), d.tx[:1], d.rx[:n]); err != nil {
		return nil, err
	}
	return d.rx[:n], nil
}

func (d *Device) fifoSize() (int, error) {
	s, err := d.read16(regFIFOStatus1)
	if err != nil {
		return 0, err
	}
	bytesCnt := int(s>>8) | int((s&0x00C0)<<2)
	return bytesCnt, nil
}

func (d *Device) waitFIFO(ctx context.Context, need, timeoutMS int) (int, error) {
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := d.fifoSize()
		if err != nil {
			return 0, err
		}
		if n >= need {
			return n, nil
		}
		if !time.Now().Before(deadline) {
			return 0, nil
		}
		if d.pollDelay > 0 {
			time.Sleep(d.pollDelay)
		}
	}
}

func (d *Device) writeFWT(timeoutMS int) error {
	ctrl, err := d.readReg(regTimerEMVControl)
	if err != nil {
		return err
	}
	step := int64(64 * 1000 * 1000)
	if ctrl&0x01 != 0 {
		step = 4096 * 1000 * 1000
	}
	us := int64(timeoutMS) * 1000
	nrt := (us*fcHz + step - 1) / step
	if nrt < 1 {
		nrt = 1
	}
	if nrt > 0xFFFF {
		nrt = 0xFFFF
	}
	return d.write16(regNoResponseTimer1, uint16(nrt))
}

func (d *Device) writeNumTX(fullBytes, extraBits int) error {
	v := uint16((fullBytes&0x1FF)<<3 | (extraBits & 0x07))
	return d.write16(regNumTransmittedBytes1, v)
}

// --- init ---

// Identity returns the ST25R3916 type and revision.
func (d *Device) Identity() (typ, rev uint8, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw, err := d.readReg(regICIdentity)
	if err != nil {
		return 0, 0, err
	}
	return (raw >> 3) & 0x1F, raw & 0x07, nil
}

// Begin resets and configures the chip for NFC-A.
func (d *Device) Begin(ctx context.Context) error {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.initChip(ctx); err != nil {
		return err
	}
	if err := d.configureNFCA(ctx); err != nil {
		return err
	}
	d.begun = true
	d.mode = modeA
	return nil
}

func (d *Device) initChip(ctx context.Context) error {
	raw, err := d.readReg(regICIdentity)
	if err != nil {
		return err
	}
	typ := (raw >> 3) & 0x1F
	rev := raw & 0x07
	if typ != validIdentifyType || rev == 0 || raw == 0xFF {
		return ErrNoChip
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := d.command(cmdSetDefault, nil); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	if err := d.command(cmdTestAccess, []byte{0x04, 0x10}); err != nil {
		return err
	}
	if err := d.write16(regIOConfig1, 0x1084); err != nil {
		return err
	}
	if err := d.writeReg(regTXDriver, (13&0x0F)<<4); err != nil {
		return err
	}
	v, err := d.readReg(regIOConfig1)
	if err != nil {
		return err
	}
	if err := d.writeReg(regIOConfig1, (v&^0x07)|0x07); err != nil {
		return err
	}
	if err := d.writeRegSpaceB(regSpaceBResistiveAM, 0x80); err != nil {
		return err
	}
	if err := d.setBits(regIOConfig2, aatEn); err != nil {
		return err
	}
	if err := d.writeRegSpaceB(regSpaceBResistiveAM, 0x00); err != nil {
		return err
	}
	if err := d.writeReg(regFieldDetectActivation, 0x13); err != nil {
		return err
	}
	if err := d.writeReg(regFieldDetectDeactiv, 0x02); err != nil {
		return err
	}
	nfcip, err := d.readReg(regNFCIP1PassiveTarget)
	if err != nil {
		return err
	}
	if err := d.writeReg(regNFCIP1PassiveTarget, (nfcip&^0xF0)|(0x05<<4)); err != nil {
		return err
	}
	if err := d.writeReg(regPassiveTargetMod, 0x5F); err != nil {
		return err
	}
	if err := d.writeRegSpaceB(regSpaceBEMDSuppression, 0x40); err != nil {
		return err
	}
	if err := d.writeReg(regAntennaTuning1, 0x82); err != nil {
		return err
	}
	if err := d.writeReg(regAntennaTuning2, 0x82); err != nil {
		return err
	}
	if err := d.setBits(regOperationControl, 0x03); err != nil {
		return err
	}
	if err := d.command(cmdClearFIFO, nil); err != nil {
		return err
	}
	if err := d.write32(regMaskMainInterrupt, maskPreAdjust); err != nil {
		return err
	}
	if err := d.clearInterrupts(); err != nil {
		return err
	}
	if err := d.enableOsc(ctx); err != nil {
		return err
	}
	if err := d.write32(regMaskMainInterrupt, 0); err != nil {
		return err
	}
	if err := d.command(cmdAdjustRegulators, nil); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	return nil
}

func (d *Device) enableOsc(ctx context.Context) error {
	op, err := d.readReg(regOperationControl)
	if err != nil {
		return err
	}
	if op&opEn == 0 {
		m, err := d.readReg(regMaskMainInterrupt)
		if err != nil {
			return err
		}
		if err := d.writeReg(regMaskMainInterrupt, m&^iOsc); err != nil {
			return err
		}
		if err := d.clearInterrupts(); err != nil {
			return err
		}
		if err := d.setBits(regOperationControl, opEn); err != nil {
			return err
		}
		deadline := time.Now().Add(25 * time.Millisecond)
		got := false
		for time.Now().Before(deadline) {
			if err := ctx.Err(); err != nil {
				return err
			}
			irq, err := d.readReg(regMainInterrupt)
			if err != nil {
				return err
			}
			if irq&iOsc != 0 {
				got = true
				break
			}
			if d.pollDelay > 0 {
				time.Sleep(d.pollDelay)
			}
		}
		if err := d.setBits(regMaskMainInterrupt, iOsc); err != nil {
			return err
		}
		if !got {
			return ErrOscillator
		}
	}
	aux, err := d.readReg(regAuxiliaryDisplay)
	if err != nil {
		return err
	}
	if aux&oscOK == 0 {
		return ErrOscillator
	}
	return nil
}

func (d *Device) configureNFCA(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.command(cmdStopAllActivities, nil); err != nil {
		return err
	}
	if err := d.clearBits(regOperationControl, wuBit); err != nil {
		return err
	}
	if err := d.writeReg(regModeDefinition, 0x09); err != nil {
		return err
	}
	if err := d.writeReg(regBitrateDefinition, 0x00); err != nil {
		return err
	}
	if err := d.writeReg(regISO14443ASettings, 0x00); err != nil {
		return err
	}
	if err := d.clearBits(regAuxiliaryDefinition, disCorr); err != nil {
		return err
	}
	for _, rv := range [...]struct{ reg, val uint8 }{
		{regOvershootConfig1, 0x40},
		{regOvershootConfig2, 0x03},
		{regUndershootConfig1, 0x40},
		{regUndershootConfig2, 0x03},
		{regCorrelatorConfig1, 0x47},
		{regCorrelatorConfig2, 0x00},
	} {
		if err := d.writeRegSpaceB(rv.reg, rv.val); err != nil {
			return err
		}
	}
	if err := d.writeReg(regReceiverConfig1, rxcfg1Z600K); err != nil {
		return err
	}
	if err := d.writeReg(regReceiverConfig2, rxcfg2); err != nil {
		return err
	}
	if err := d.writeReg(regReceiverConfig3, rxcfg3); err != nil {
		return err
	}
	if err := d.writeReg(regReceiverConfig4, rxcfg4); err != nil {
		return err
	}
	if err := d.write32(regMaskMainInterrupt, 0); err != nil {
		return err
	}
	if err := d.command(cmdResetRXGain, nil); err != nil {
		return err
	}
	if err := d.command(cmdNFCInitialFieldOn, nil); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	if err := d.setBits(regOperationControl, txEn|rxEn); err != nil {
		return err
	}
	return nil
}

// RFOff turns the reader field off.
func (d *Device) RFOff() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.command(cmdStopAllActivities, nil); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	return d.clearBits(regOperationControl, txEn|rxEn)
}

// RFOn turns the reader field on.
func (d *Device) RFOn() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fieldOn()
}

func (d *Device) fieldOn() error {
	if err := d.command(cmdNFCInitialFieldOn, nil); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	return d.setBits(regOperationControl, txEn|rxEn)
}

var bgContext = context.Background()

// ConfigureA switches a begun device to NFC-A (ISO14443-A).
func (d *Device) ConfigureA(ctx context.Context) error {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return ErrNotBegun
	}
	if err := d.configureNFCA(ctx); err != nil {
		return err
	}
	d.mode = modeA
	return nil
}

// ConfigureB switches a begun device to NFC-B (ISO14443-B, 106 kbps).
func (d *Device) ConfigureB(ctx context.Context) error {
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
	for i, v := range nfcBTable {
		if err := d.writeReg(uint8(i), v); err != nil {
			return err
		}
	}
	for _, rv := range [...]struct{ reg, val uint8 }{
		{0x05, 0xC4}, {0x06, 0x14}, {0x0B, 0x0C}, {0x0C, 0x1B},
		{0x0D, 0x00}, {0x0F, 0x00}, {0x15, 0x00}, {0x28, 0x10},
		{0x29, 0x7C}, {0x2A, 0x80}, {0x2B, 0x04}, {0x2C, 0xD0},
	} {
		if err := d.writeRegSpaceB(rv.reg, rv.val); err != nil {
			return err
		}
	}
	if err := d.command(cmdNFCInitialFieldOn, nil); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	d.mode = modeB
	return nil
}

// nfcBTable is the working NFC-B register set from the official driver.
var nfcBTable = [...]uint8{
	0x07, 0x3C, 0xCB, 0x14, 0x00, 0x00, 0x00, 0x00, 0x5D, 0x38, 0x00, 0x04, 0x3D, 0x00, 0x00, 0x07,
	0x10, 0x0D, 0x23, 0x00, 0x58, 0x80, 0x85, 0xA6, 0x0F, 0x7B, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00,
	0x00, 0x00, 0x00, 0x48, 0x00, 0xE1, 0x82, 0x82, 0x70, 0x5F, 0x13, 0x02, 0x00, 0xAB, 0x38, 0x00,
	0x00, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
}

// ConfigureF switches a begun device to NFC-F (FeliCa, 212 kbps).
func (d *Device) ConfigureF(ctx context.Context) error {
	ctx = ctxOrBackground(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.begun {
		return ErrNotBegun
	}
	if err := d.configureNFCF(ctx); err != nil {
		return err
	}
	d.mode = modeF
	return nil
}

func (d *Device) configureNFCF(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.command(cmdStopAllActivities, nil); err != nil {
		return err
	}
	if err := d.writeReg(regModeDefinition, 0x1C); err != nil { // initiator, FeliCa | tr_am (0x04)
		return err
	}
	if err := d.writeReg(regBitrateDefinition, 0x11); err != nil { // 212 kbps TX/RX
		return err
	}
	if err := d.clearBits(regOperationControl, 0x03); err != nil { // field detector off
		return err
	}
	if err := d.writeReg(regIOConfig1, 0x07); err != nil {
		return err
	}
	if err := d.writeReg(regAuxiliaryDefinition, 0x00); err != nil {
		return err
	}
	for _, rv := range [...]struct{ reg, val uint8 }{
		{regReceiverConfig1, 0x13},
		{regReceiverConfig2, 0x3D},
		{regReceiverConfig3, 0x00},
		{regReceiverConfig4, 0x00},
	} {
		if err := d.writeReg(rv.reg, rv.val); err != nil {
			return err
		}
	}
	if err := d.writeRegSpaceB(regCorrelatorConfig1, 0x54); err != nil {
		return err
	}
	if err := d.writeRegSpaceB(regCorrelatorConfig2, 0x00); err != nil {
		return err
	}
	if err := d.write32(regMaskMainInterrupt, 0); err != nil {
		return err
	}
	if err := d.command(cmdNFCInitialFieldOn, nil); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	return d.setBits(regOperationControl, txEn|rxEn)
}

// Mode returns the active protocol (0 none, 1 NFC-A, 2 NFC-F).
func (d *Device) Mode() uint8 {
	d.mu.Lock()
	m := d.mode
	d.mu.Unlock()
	return m
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return bgContext
	}
	return ctx
}
