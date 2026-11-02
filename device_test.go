package nfc

import (
	"context"
	"errors"
	"testing"
	"time"
	"unsafe"
)

// fakeChip emulates the ST25R3916 registers/FIFO and one NFC-A PICC.
var errNoAck = errors.New("i2c: no ack")

type fakeChip struct {
	regs    [256]byte
	spaceB  [64]byte
	fifo    [64]byte
	fifoLen int
	txbuf   [16]byte
	txLen   int
	tx      int
	fail    bool

	tag     bool
	fcTag   bool
	idm     [8]byte
	pmm     [8]byte
	t2      [16]byte
	vTag    bool
	vResp   [64]byte
	vLen    int
	bTag    bool
	pupi    [4]byte
	app     [4]byte
	proto   [3]byte
	atqa    uint16
	uid     [7]byte
	uidLen  int
	sak     uint8
	cascade bool
}

func newFakeChip() *fakeChip {
	f := &fakeChip{}
	f.regs[regICIdentity] = 0x29 // type 5, rev 1
	f.regs[regAuxiliaryDisplay] = oscOK
	return f
}

func (f *fakeChip) setTag4(uid [4]byte, atqa uint16, sak uint8) {
	f.tag = true
	f.uidLen = 4
	f.uid = [7]byte{uid[0], uid[1], uid[2], uid[3]}
	f.atqa = atqa
	f.sak = sak
}

func (f *fakeChip) setTagV(resp []byte) {
	f.vTag = true
	copy(f.vResp[:], resp)
	f.vLen = len(resp)
}

func (f *fakeChip) setTagB(pupi, app [4]byte, proto [3]byte) {
	f.bTag = true
	f.pupi = pupi
	f.app = app
	f.proto = proto
}

func (f *fakeChip) setTagF(idm, pmm [8]byte) {
	f.fcTag = true
	f.idm = idm
	f.pmm = pmm
}

func (f *fakeChip) setTag7(uid [7]byte, atqa uint16, sak uint8) {
	f.tag = true
	f.uidLen = 7
	f.uid = uid
	f.atqa = atqa
	f.sak = sak
}

func bcc(b0, b1, b2, b3 byte) byte { return b0 ^ b1 ^ b2 ^ b3 }

func (f *fakeChip) command(cmd uint8, payload []byte) {
	switch cmd {
	case cmdClearFIFO:
		f.fifoLen = 0
	case cmdTransmitREQA, cmdTransmitWUPA:
		if f.tag {
			f.fifo[0] = byte(f.atqa)
			f.fifo[1] = byte(f.atqa >> 8)
			f.fifoLen = 2
		}
	case cmdTransmitWithoutCRC:
		if f.vTag && f.txLen > 0 && f.txbuf[0] == 0x21 { // NFC-V VCD frame
			copy(f.fifo[:], f.vResp[:f.vLen])
			f.fifoLen = f.vLen
			return
		}
		if !f.tag || f.txLen < 2 {
			return
		}
		switch f.txbuf[0] {
		case 0x93:
			if f.uidLen == 7 {
				f.fifo[0] = 0x88
				f.fifo[1] = f.uid[0]
				f.fifo[2] = f.uid[1]
				f.fifo[3] = f.uid[2]
				f.fifo[4] = bcc(0x88, f.uid[0], f.uid[1], f.uid[2])
			} else {
				f.fifo[0] = f.uid[0]
				f.fifo[1] = f.uid[1]
				f.fifo[2] = f.uid[2]
				f.fifo[3] = f.uid[3]
				f.fifo[4] = bcc(f.uid[0], f.uid[1], f.uid[2], f.uid[3])
			}
			f.fifoLen = 5
		case 0x95:
			f.fifo[0] = f.uid[3]
			f.fifo[1] = f.uid[4]
			f.fifo[2] = f.uid[5]
			f.fifo[3] = f.uid[6]
			f.fifo[4] = bcc(f.uid[3], f.uid[4], f.uid[5], f.uid[6])
			f.fifoLen = 5
		}
	case cmdTransmitWithCRC:
		if f.txLen < 2 {
			return
		}
		if f.txbuf[0] == 0x50 {
			return
		}
		if f.txbuf[0] == 0x30 { // Type 2 READ
			copy(f.fifo[0:16], f.t2[:])
			f.fifo[16] = 0
			f.fifo[17] = 0
			f.fifoLen = 18
			return
		}
		if f.txbuf[0] == 0x05 && f.txLen == 3 { // NFC-B REQB
			if !f.bTag {
				return
			}
			f.fifo[0] = 0x50
			copy(f.fifo[1:5], f.pupi[:])
			copy(f.fifo[5:9], f.app[:])
			copy(f.fifo[9:12], f.proto[:])
			f.fifo[12] = 0
			f.fifo[13] = 0
			f.fifoLen = 14
			return
		}
		if f.txbuf[0] == 0x00 && f.txLen == 5 { // NFC-F Polling
			if !f.fcTag {
				return
			}
			f.fifo[0] = 18
			f.fifo[1] = 0x01
			copy(f.fifo[2:10], f.idm[:])
			copy(f.fifo[10:18], f.pmm[:])
			f.fifoLen = 18
			return
		}
		if !f.tag {
			return
		}
		f.fifo[0] = f.sak
		if f.txbuf[0] == 0x93 && f.uidLen == 7 {
			f.fifo[0] = 0x04 // cascade bit after the CL1 select
		}
		f.fifoLen = 1
	}
}

func (f *fakeChip) Tx(addr uint16, w, r []byte) error {
	if f.fail || addr != DefaultAddress {
		return errNoAck
	}
	f.tx++
	if len(w) == 0 {
		return nil
	}
	b0 := w[0]
	switch {
	case b0 == opLoadFIFO:
		copy(f.txbuf[:], w[1:])
		f.txLen = len(w) - 1
		return nil
	case b0 == opReadFIFO:
		copy(r, f.fifo[:f.fifoLen])
		return nil
	case b0 == cmdSpaceB:
		if len(r) > 0 {
			r[0] = f.spaceB[w[1]&0x3F]
		} else if len(w) >= 3 {
			f.spaceB[w[1]&0x3F] = w[2]
		}
		return nil
	case b0 >= 0xC0:
		f.command(b0, w[1:])
		return nil
	}
	if len(r) > 0 {
		reg := b0 &^ opRead
		switch reg {
		case regFIFOStatus1:
			r[0] = byte(f.fifoLen)
		case regFIFOStatus1 + 1:
			r[0] = 0
		default:
			r[0] = f.regs[reg]
		}
		return nil
	}
	if len(w) >= 2 {
		f.regs[b0] = w[1]
		if b0 == regOperationControl && w[1]&opEn != 0 {
			f.regs[regMainInterrupt] |= iOsc
			f.regs[regAuxiliaryDisplay] |= oscOK
		}
	}
	return nil
}

func newTestDevice(tb testing.TB) (*Device, *fakeChip) {
	tb.Helper()
	f := newFakeChip()
	d := New(f)
	d.pollDelay = 0
	d.cmdDelay = 0
	if err := d.Begin(context.Background()); err != nil {
		tb.Fatalf("Begin: %v", err)
	}
	return d, f
}

func TestIdentity(t *testing.T) {
	f := newFakeChip()
	d := New(f)
	typ, rev, err := d.Identity()
	if err != nil || typ != 5 || rev != 1 {
		t.Fatalf("Identity = %d, %d, %v", typ, rev, err)
	}
}

func TestBegin(t *testing.T) {
	d, _ := newTestDevice(t)
	if !d.begun {
		t.Fatal("device not marked as begun")
	}
}

func TestBeginBadChip(t *testing.T) {
	f := newFakeChip()
	f.regs[regICIdentity] = 0x00
	d := New(f)
	d.pollDelay = 0
	d.cmdDelay = 0
	if err := d.Begin(context.Background()); !errors.Is(err, ErrNoChip) {
		t.Fatalf("Begin = %v, want ErrNoChip", err)
	}
}

func TestDetectClassic(t *testing.T) {
	d, f := newTestDevice(t)
	uid := [4]byte{0xDE, 0xAD, 0xBE, 0xEF}
	f.setTag4(uid, 0x0004, 0x08)
	c, ok, err := d.Detect(context.Background())
	if err != nil || !ok {
		t.Fatalf("Detect = %v, %v", ok, err)
	}
	if c.UIDLen != 4 || c.UID != [7]byte{0xDE, 0xAD, 0xBE, 0xEF} {
		t.Fatalf("UID = %x len %d", c.UID, c.UIDLen)
	}
	if c.ATQA != 0x0004 || c.SAK != 0x08 {
		t.Fatalf("ATQA/SAK = %#x/%#x", c.ATQA, c.SAK)
	}
	if c.TypeID != 2 || c.UserMemory != 752 || !c.IsClassic() {
		t.Fatalf("type = %d memory = %d", c.TypeID, c.UserMemory)
	}
}

func TestDetectCascade(t *testing.T) {
	d, f := newTestDevice(t)
	uid := [7]byte{0x04, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	f.setTag7(uid, 0x0044, 0x00)
	c, ok, err := d.Detect(context.Background())
	if err != nil || !ok {
		t.Fatalf("Detect = %v, %v", ok, err)
	}
	if c.UIDLen != 7 || c.UID != uid {
		t.Fatalf("UID = %x len %d", c.UID, c.UIDLen)
	}
	if c.TypeID != 5 || !c.IsType2() {
		t.Fatalf("type = %d", c.TypeID)
	}
}

func TestCrypto1ProxmarkVector(t *testing.T) {
	const key = uint64(0xFFFFFFFFFFFF)
	uid := uint32(0x9C599B32)
	nt := uint32(0x82A4166C)
	nrEnc := uint32(0xA1E458CE)

	var c1 Crypto1
	c1.init(key)
	c1.inject(uid, nt)
	ks1 := c1.step32In(nrEnc, true)
	nr := nrEnc ^ ks1
	ar, _ := suc23(byteswap32(nt))

	var c2 Crypto1
	c2.init(key)
	c2.inject(uid, nt)
	ab, _ := c2.encryptAB(nr, ar)
	gotNR := uint32(ab[0])<<24 | uint32(ab[1])<<16 | uint32(ab[2])<<8 | uint32(ab[3])
	gotAR := uint32(ab[4])<<24 | uint32(ab[5])<<16 | uint32(ab[6])<<8 | uint32(ab[7])
	if gotNR != 0xA1E458CE || gotAR != 0x6EEA41E0 {
		t.Fatalf("Crypto1 vector = %08X %08X", gotNR, gotAR)
	}
}

func TestType2Read(t *testing.T) {
	d, f := newTestDevice(t)
	f.setTag7([7]byte{0x04, 1, 2, 3, 4, 5, 6}, 0x0044, 0x00)
	if _, ok, _ := d.Detect(context.Background()); !ok {
		t.Fatal("no tag")
	}
	for i := range f.t2 {
		f.t2[i] = byte(i)
	}
	q, ok, err := d.Type2ReadQuad(context.Background(), 0)
	if err != nil || !ok {
		t.Fatalf("Type2ReadQuad = %v, %v", ok, err)
	}
	for i := range q {
		if q[i] != byte(i) {
			t.Fatalf("quad[%d] = %02X", i, q[i])
		}
	}
	p, ok, err := d.Type2ReadPage(context.Background(), 5)
	if err != nil || !ok {
		t.Fatalf("Type2ReadPage = %v, %v", ok, err)
	}
	if p != [4]byte{4, 5, 6, 7} {
		t.Fatalf("page 5 = %v", p)
	}
}

func TestDetectF(t *testing.T) {
	d, f := newTestDevice(t)
	idm := [8]byte{0x01, 0x4B, 0x4F, 0x43, 0x20, 0x00, 0x00, 0x00}
	pmm := [8]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	f.setTagF(idm, pmm)
	if _, _, err := d.DetectF(context.Background()); !errors.Is(err, ErrWrongMode) {
		t.Fatalf("DetectF before ConfigureF = %v", err)
	}
	if err := d.ConfigureF(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, ok, err := d.DetectF(context.Background())
	if err != nil || !ok {
		t.Fatalf("DetectF = %v, %v", ok, err)
	}
	if c.IDm != idm || c.PMm != pmm {
		t.Fatalf("FeliCa = %X %X", c.IDm, c.PMm)
	}
	if d.Mode() != modeF {
		t.Fatalf("mode = %d", d.Mode())
	}
	if err := d.ConfigureA(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.setTag4([4]byte{1, 2, 3, 4}, 0x0004, 0x08)
	if _, ok, err := d.Detect(context.Background()); err != nil || !ok {
		t.Fatalf("Detect after ConfigureA = %v, %v", ok, err)
	}
}

func TestDetectFNone(t *testing.T) {
	d, _ := newTestDevice(t)
	if err := d.ConfigureF(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.DetectF(context.Background()); err != nil || ok {
		t.Fatalf("DetectF no card = %v, %v", ok, err)
	}
}

type bitWriter struct {
	buf   [64]byte
	nbits int
}

func (w *bitWriter) bit(b bool) {
	if b {
		w.buf[w.nbits/8] |= 1 << (w.nbits % 8)
	}
	w.nbits++
}

func (w *bitWriter) bitsLSB(v uint32, n int) {
	for i := 0; i < n; i++ {
		w.bit(v>>uint(i)&1 != 0)
	}
}

// buildVICC mirrors the official test helper: SOF 0x17 (5 bits), Manchester
// payload and an EOF pattern at the byte boundary the decoder checks.
func buildVICC(payload []byte) []byte {
	var w bitWriter
	w.bitsLSB(0x17, 5)
	for _, b := range payload {
		for i := 0; i < 8; i++ {
			if b>>uint(i)&1 != 0 {
				w.bit(false)
				w.bit(true)
			} else {
				w.bit(true)
				w.bit(false)
			}
		}
	}
	mpEnd := 5 + len(payload)*8*2
	bytePos := mpEnd / 8
	if len(w.buf) <= bytePos+1 {
		return nil
	}
	w.buf[bytePos] = (w.buf[bytePos] & 0x1F) | 0xA0
	w.buf[bytePos+1] = 0x03
	n := bytePos + 2
	return append([]byte(nil), w.buf[:n]...)
}

func TestVICCRoundTrip(t *testing.T) {
	payload := []byte{0x00, 0xE0, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x04, 0xE0}
	frame := buildVICC(payload)
	if frame == nil {
		t.Fatal("frame too long")
	}
	var out [16]byte
	n, ok := decodeVICC(out[:], frame, 0)
	if !ok || n != len(payload) {
		t.Fatalf("decode = %d, %v", n, ok)
	}
	for i := range payload {
		if out[i] != payload[i] {
			t.Fatalf("decode[%d] = %02X, want %02X", i, out[i], payload[i])
		}
	}
}

func TestEncodeVCD(t *testing.T) {
	frame := []byte{0x26, 0x01, 0x00, 0x00, 0x00}
	var enc [32]byte
	n := encodeVCD(enc[:], frame)
	if n != 1+len(frame)*4+1 || enc[0] != vcdSof || enc[n-1] != vcdEof {
		t.Fatalf("encodeVCD = %d bytes, sof/eof %02X/%02X", n, enc[0], enc[n-1])
	}
}

func TestDetectV(t *testing.T) {
	d, f := newTestDevice(t)
	uid := [8]byte{0xE0, 0x04, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	payload := []byte{0x00, 0xE0,
		uid[7], uid[6], uid[5], uid[4], uid[3], uid[2], uid[1], uid[0]}
	f.setTagV(buildVICC(payload))
	if _, _, err := d.DetectV(context.Background()); !errors.Is(err, ErrWrongMode) {
		t.Fatalf("DetectV before ConfigureV = %v", err)
	}
	if err := d.ConfigureV(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, ok, err := d.DetectV(context.Background())
	if err != nil || !ok {
		t.Fatalf("DetectV = %v, %v", ok, err)
	}
	if c.UID != uid || c.DSFID != 0xE0 {
		t.Fatalf("NFC-V = %+v", c)
	}
	if d.Mode() != modeV {
		t.Fatalf("mode = %d", d.Mode())
	}
}

func TestDetectNoneV(t *testing.T) {
	d, _ := newTestDevice(t)
	if err := d.ConfigureV(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.DetectV(context.Background()); err != nil || ok {
		t.Fatalf("DetectV no card = %v, %v", ok, err)
	}
}

func TestDetectB(t *testing.T) {
	d, f := newTestDevice(t)
	pupi := [4]byte{0x11, 0x22, 0x33, 0x44}
	app := [4]byte{0x00, 0x00, 0x00, 0x00}
	proto := [3]byte{0x01, 0x00, 0x00}
	f.setTagB(pupi, app, proto)
	if _, _, err := d.DetectB(context.Background()); !errors.Is(err, ErrWrongMode) {
		t.Fatalf("DetectB before ConfigureB = %v", err)
	}
	if err := d.ConfigureB(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, ok, err := d.DetectB(context.Background())
	if err != nil || !ok {
		t.Fatalf("DetectB = %v, %v", ok, err)
	}
	if c.PUPI != pupi || c.Application != app || c.Protocol != proto {
		t.Fatalf("NFC-B = %+v", c)
	}
	if d.Mode() != modeB {
		t.Fatalf("mode = %d", d.Mode())
	}
}

func TestDetectBNone(t *testing.T) {
	d, _ := newTestDevice(t)
	if err := d.ConfigureB(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.DetectB(context.Background()); err != nil || ok {
		t.Fatalf("DetectB no card = %v, %v", ok, err)
	}
}

func TestDetectNone(t *testing.T) {
	d, _ := newTestDevice(t)
	c, ok, err := d.Detect(context.Background())
	if err != nil || ok {
		t.Fatalf("Detect no tag = %v, %v, %+v", ok, err, c)
	}
}

func TestDetectCancel(t *testing.T) {
	d, _ := newTestDevice(t)
	d.pollDelay = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := d.Detect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Detect canceled = %v", err)
	}
}

func TestFWT(t *testing.T) {
	d, f := newTestDevice(t)
	if err := d.writeFWT(12); err != nil {
		t.Fatal(err)
	}
	got := uint16(f.regs[regNoResponseTimer1])<<8 | uint16(f.regs[regNoResponseTimer1+1])
	// 12 ms * 13.56 MHz / 64e6, rounded up.
	want := uint16((12*1000*13560000 + 64*1000*1000 - 1) / (64 * 1000 * 1000))
	if got != want {
		t.Fatalf("NRT = %d, want %d", got, want)
	}
}

func TestHalt(t *testing.T) {
	d, f := newTestDevice(t)
	f.setTag4([4]byte{1, 2, 3, 4}, 0x0004, 0x08)
	if _, ok, _ := d.Detect(context.Background()); !ok {
		t.Fatal("no tag")
	}
	if err := d.Halt(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBeginNoAlloc(t *testing.T) {
	f := newFakeChip()
	d := New(f)
	d.pollDelay = 0
	d.cmdDelay = 0
	ctx := context.Background()
	if allocs := testing.AllocsPerRun(5, func() { _ = d.Begin(ctx) }); allocs != 0 {
		t.Fatalf("Begin allocates %v per call", allocs)
	}
}

func TestNoAlloc(t *testing.T) {
	d, f := newTestDevice(t)
	f.setTag4([4]byte{1, 2, 3, 4}, 0x0004, 0x08)
	ctx := context.Background()
	if allocs := testing.AllocsPerRun(10, func() {
		_, _, _ = d.Detect(ctx)
	}); allocs != 0 {
		t.Fatalf("Detect allocates %v per call", allocs)
	}
	if err := d.ConfigureF(ctx); err != nil {
		t.Fatal(err)
	}
	f.setTagF([8]byte{1, 2, 3, 4, 5, 6, 7, 8}, [8]byte{})
	if allocs := testing.AllocsPerRun(10, func() {
		_, _, _ = d.DetectF(ctx)
	}); allocs != 0 {
		t.Fatalf("DetectF allocates %v per call", allocs)
	}
	if err := d.ConfigureB(ctx); err != nil {
		t.Fatal(err)
	}
	f.setTagB([4]byte{1, 2, 3, 4}, [4]byte{}, [3]byte{})
	if allocs := testing.AllocsPerRun(10, func() {
		_, _, _ = d.DetectB(ctx)
	}); allocs != 0 {
		t.Fatalf("DetectB allocates %v per call", allocs)
	}
	uid := [8]byte{0xE0, 0x04, 1, 2, 3, 4, 5, 6}
	payload := []byte{0, 0xE0, uid[7], uid[6], uid[5], uid[4], uid[3], uid[2], uid[1], uid[0]}
	f.setTagV(buildVICC(payload))
	f.setTag4([4]byte{1, 2, 3, 4}, 0x0004, 0x08)
	if err := d.ConfigureA(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := d.Detect(ctx); !ok {
		t.Fatal("no tag for Type2")
	}
	if allocs := testing.AllocsPerRun(10, func() {
		_, _, _ = d.Type2ReadPage(ctx, 4)
	}); allocs != 0 {
		t.Fatalf("Type2ReadPage allocates %v per call", allocs)
	}
	if err := d.ConfigureV(ctx); err != nil {
		t.Fatal(err)
	}
	if allocs := testing.AllocsPerRun(10, func() {
		_, _, _ = d.DetectV(ctx)
	}); allocs != 0 {
		t.Fatalf("DetectV allocates %v per call", allocs)
	}
}

func TestFootprint(t *testing.T) {
	t.Logf("Card=%d B  Device=%d B", unsafe.Sizeof(Card{}), unsafe.Sizeof(Device{}))
	if n := unsafe.Sizeof(Device{}); n > 384 {
		t.Fatalf("Device is %d B, budget 384 B", n)
	}
}
