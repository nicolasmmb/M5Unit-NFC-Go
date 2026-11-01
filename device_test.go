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
		if !f.tag || f.txLen < 2 {
			return
		}
		if f.txbuf[0] == 0x50 {
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
	allocs := testing.AllocsPerRun(10, func() {
		_, _, _ = d.Detect(ctx)
	})
	if allocs != 0 {
		t.Fatalf("Detect allocates %v per call", allocs)
	}
}

func TestFootprint(t *testing.T) {
	t.Logf("Card=%d B  Device=%d B", unsafe.Sizeof(Card{}), unsafe.Sizeof(Device{}))
	if n := unsafe.Sizeof(Device{}); n > 384 {
		t.Fatalf("Device is %d B, budget 384 B", n)
	}
}
