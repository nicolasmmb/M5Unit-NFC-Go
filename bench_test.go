package nfc

import (
	"context"
	"testing"
	"time"
)

func reportTx(b *testing.B, f *fakeChip, before int) {
	b.Helper()
	b.ReportMetric(float64(f.tx-before)/float64(b.N), "tx/op")
}

func BenchmarkBegin(b *testing.B) {
	f := newFakeChip()
	d := New(f)
	d.pollDelay = 0
	d.cmdDelay = 0
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	start := f.tx
	for i := 0; i < b.N; i++ {
		if err := d.Begin(ctx); err != nil {
			b.Fatal(err)
		}
	}
	reportTx(b, f, start)
}

func BenchmarkDetect(b *testing.B) {
	d, f := newTestDevice(b)
	d.pollDelay = time.Millisecond
	f.setTag4([4]byte{0xDE, 0xAD, 0xBE, 0xEF}, 0x0004, 0x08)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	start := f.tx
	for i := 0; i < b.N; i++ {
		if _, ok, err := d.Detect(ctx); err != nil || !ok {
			b.Fatalf("Detect: %v %v", ok, err)
		}
	}
	reportTx(b, f, start)
}

// BenchmarkDetectRealistic keeps the firmware-like command gaps (2 ms) and
// 1 ms polling, so ns/op reflects the real detection latency.
func BenchmarkDetectRealistic(b *testing.B) {
	f := newFakeChip()
	d := New(f)
	if err := d.Begin(context.Background()); err != nil {
		b.Fatal(err)
	}
	f.setTag4([4]byte{0xDE, 0xAD, 0xBE, 0xEF}, 0x0004, 0x08)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	start := f.tx
	for i := 0; i < b.N; i++ {
		if _, ok, err := d.Detect(ctx); err != nil || !ok {
			b.Fatalf("Detect: %v %v", ok, err)
		}
	}
	reportTx(b, f, start)
}

func BenchmarkDetectF(b *testing.B) {
	d, f := newTestDevice(b)
	d.pollDelay = time.Millisecond
	f.setTagF([8]byte{1, 2, 3, 4, 5, 6, 7, 8}, [8]byte{})
	if err := d.ConfigureF(context.Background()); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	start := f.tx
	for i := 0; i < b.N; i++ {
		if _, ok, err := d.DetectF(ctx); err != nil || !ok {
			b.Fatalf("DetectF: %v %v", ok, err)
		}
	}
	reportTx(b, f, start)
}

func BenchmarkDetectB(b *testing.B) {
	d, f := newTestDevice(b)
	d.pollDelay = time.Millisecond
	f.setTagB([4]byte{1, 2, 3, 4}, [4]byte{}, [3]byte{})
	if err := d.ConfigureB(context.Background()); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	start := f.tx
	for i := 0; i < b.N; i++ {
		if _, ok, err := d.DetectB(ctx); err != nil || !ok {
			b.Fatalf("DetectB: %v %v", ok, err)
		}
	}
	reportTx(b, f, start)
}

func BenchmarkDetectNoCard(b *testing.B) {
	d, f := newTestDevice(b)
	d.pollDelay = time.Millisecond
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	start := f.tx
	for i := 0; i < b.N; i++ {
		if _, ok, err := d.Detect(ctx); err != nil || ok {
			b.Fatalf("Detect: %v %v", ok, err)
		}
	}
	reportTx(b, f, start)
}
