# Benchmarks

Phase 1 target: ST25R3916 core + NFC-A probe. Benchmarks run on the host
against a register/FIFO fake (`fakeChip`); `tx/op` is the quantity that
dominates on real hardware (400 kHz I2C, ~60–80 µs per transaction).

## Method

| Benchmark | What it measures |
|---|---|
| `BenchmarkBegin` | identity check + full init/regulator/oscillator/config sequence |
| `BenchmarkDetect` | NFC-A activation against a scripted PICC (zero fake delays) |
| `BenchmarkDetectRealistic` | same with the firmware's 2 ms command gaps + 1 ms polling |
| `BenchmarkDetectF` | FeliCa polling (212 kbps) against a scripted PICC |
| `BenchmarkDetectB` | NFC-B REQB/ATQB against a scripted PICC |
| `BenchmarkDetectNoCard` | two timeouts (REQA + WUPA) and the polling cost |
| `TestNoAlloc` / `TestBeginNoAlloc` | 0 allocations per operation |
| `TestFootprint` | `Card` and `Device` sizes |

## How to run

```bash
make test
make bench
make alloc
make save NAME=before
make bench-compare FROM=before TO=after
```

## Results (Apple M2 Pro, `-count=6`)

| Benchmark | ns/op | tx/op | allocs |
|---|---:|---:|---:|
| `Begin` | 22.7 ms | 64.2 | 0 |
| `Detect` (fake, no delays) | 124 µs | 51.0 | 0 |
| `DetectRealistic` | 13.9 ms | 51.0 | 0 |
| `DetectF` | 62 µs | 17.0 | 0 |
| `DetectB` | 61.5 µs | 17.0 | 0 |
| `DetectNoCard` | 25.2 ms | 72.0 | 0 |

- `Device` = **152 B**; `Card` = **14 B**.
- `Begin` is dominated by the 10 ms SET_DEFAULT wait and regulator/oscillator
  settling; `DetectNoCard` is two 12 ms FWT timeouts.
- All operations are allocation-free: commands, FIFO frames and the card are
  value/scratch buffers owned by the device.

Real hardware adds the I2C transaction time; at 400 kHz each transaction is
~60–80 µs, so `Detect` is expected to be in the 15–25 ms range with a card
present and ~25–30 ms without one (the fake numbers above already include the
firmware-like gaps in `DetectRealistic`).

## Next phases

NFC-F/V/B probe, Type 2/Classic read/write and the remaining C++ parity will
add their own benchmarks (`tx/op`, CPU for Crypto1/AES, memory) with
before/after comparisons.
