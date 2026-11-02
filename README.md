# M5Unit-NFC-Go

[![test](https://github.com/nicolasmmb/M5Unit-NFC-Go/actions/workflows/test.yml/badge.svg)](https://github.com/nicolasmmb/M5Unit-NFC-Go/actions/workflows/test.yml)

Pure-Go driver for the **M5Stack Unit NFC** (SKU **U216**, **ST25R3916**).
No C, C++, CGo or Arduino.

**Phase 1:** ST25R3916 core + **NFC-A** (UID/type), **NFC-F** (FeliCa
IDm/PMm — FeliCa IC cards) and **NFC-B** (ATQB/PUPI) probes.
NFC-V, read/write, NDEF and emulation are the next phases (see Roadmap).

## Features

- Full ST25R3916 initialization from the official sequence: identity,
  SET_DEFAULT, test access, IO/TX driver, space-B registers, field detector,
  antenna tuning, oscillator and regulator adjustment.
- I2C transport at **0x50**, including the **16-bit space-B** register space.
- NFC-A: REQA/WUPA, anticollision cascade CL1/CL2, SELECT, HLTA.
- NFC-F: FeliCa polling at 212 kbps (wildcard), IDm/PMm, system-code list,
  generic type (Standard/Lite/Lite-S/Plug) and Read Without Encryption.
- NFC-B: REQB/ATQB, PUPI, application and protocol bytes.
- NFC-V: ISO15693 inventory (1-of-4 VCD encoder and Manchester decoder), UID/DSFID.
- **Type 2 read** (Ultralight/NTAG): READ 0x30, quad/page extraction.
- **MIFARE Classic**: Crypto1 (Proxmark vector validated), key-A authentication,
  encrypted block read/write with read-back verification.
- **Type 2 write**: WRITE 0xA2 + ACK + read-back verification.
- `Card` (NFC-A): UID (4/7 bytes), ATQA, SAK, type id/name and user memory.
- `FeliCaCard` (NFC-F): IDm and PMm.
- `Begin`/`Detect`/`Halt`/`RFOn`/`RFOff`, timeouts and `context` cancellation.
- **Zero allocations per operation** and fixed scratch buffers.
- Host tests with a register/FIFO/PICC emulator and benchmarks with `tx/op`.

## Hardware

| Parameter | Value |
|---|---|
| Unit | M5Stack Unit NFC (U216) |
| Chip | ST25R3916 (13.56 MHz NFC front end) |
| Interface | I2C **0x50**, 400 kHz |
| Wiring (M5StickC) | Grove I2C1: **SDA=G32, SCL=G33** |
| Protocols | NFC-A (this phase); NFC-F/V/B later |

The Grove port is shared with the other units (Encoder, GPS, ToF, Joystick2):
connect only one at a time.

## Install

```bash
go get github.com/nicolasmmb/M5Unit-NFC-Go@v0.1.0
```

## Quick start

```go
i2c := machine.I2C1
_ = i2c.Configure(machine.I2CConfig{
    SDA:       machine.GPIO32,
    SCL:       machine.GPIO33,
    Frequency: 400 * machine.KHz,
})

d := nfc.New(i2c)
if err := d.Begin(context.Background()); err != nil {
    println("nfc:", err.Error())
}

card, ok, err := d.Detect(context.Background())
if err == nil && ok {
    println(nfc.TypeName(card.TypeID), card.UID[:card.UIDLen])
}
```

## API

| Function | Description |
|---|---|
| `New(bus I2C) *Device` | Driver on any bus implementing `Tx(addr, w, r)` |
| `Identity() (typ, rev, error)` | ST25R3916 identity register |
| `Begin(ctx) error` | Reset and configure for NFC-A |
| `Detect(ctx) (Card, bool, error)` | Activate one NFC-A card; `ok=false` when idle |
| `ConfigureA(ctx)` / `ConfigureF(ctx)` | Switch a begun device between NFC-A and NFC-F |
| `ConfigureB(ctx)` | Switch a begun device to NFC-B |
| `DetectF(ctx) (FeliCaCard, bool, error)` | FeliCa polling (IDm/PMm) |
| `DetectFInfo(ctx) (FeliCaInfo, bool, error)` | Polling + system codes + type |
| `FeliCaReadBlock(ctx, info, service, block)` | Read Without Encryption (16 B) |
| `DetectB(ctx) (NFCBCard, bool, error)` | NFC-B REQB/ATQB |
| `ConfigureV(ctx)` / `DetectV(ctx)` | NFC-V ISO15693 inventory |
| `Type2ReadPage(ctx, page)` / `Type2ReadQuad(ctx, base)` | Ultralight/NTAG read |
| `ClassicReadBlock(ctx, uid, block, key)` | Crypto1 key-A auth + encrypted read |
| `Type2WritePage(ctx, page, data)` | Page write + verification |
| `ClassicWriteBlock(ctx, uid, block, key, data)` | Auth + write + verify (never UID/trailer) |
| `Mode() uint8` | Active protocol (1 = A, 2 = F) |
| `Halt(ctx) error` | Send HLTA |
| `RFOn()` / `RFOff()` | Field control |
| `TypeName(id) string` | Label for the type table |

`Card`: `UID [7]byte`, `UIDLen`, `ATQA`, `SAK`, `TypeID`, `UserMemory`.
Helpers: `IsClassic()`, `IsType2()`.

## Performance

See [BENCHMARKS.md](BENCHMARKS.md). Host measurements:

| Operation | Result |
|---|---:|
| `Begin` | 22.7 ms, 64 tx |
| `Detect` (fake, no gaps) | 124 µs, 51 tx |
| `Detect` (firmware-like gaps) | 13.9 ms, 51 tx |
| `Detect` no card | 25.2 ms, 72 tx |
| `DetectF` (FeliCa) | 62 µs, 17 tx |
| `DetectB` (NFC-B) | 61.5 µs, 17 tx |
| `DetectV` (NFC-V) | 61.8 µs, 15 tx |
| `Type2ReadPage` | (see BENCHMARKS) |
| allocations | 0 per op |
| `Device` / `Card` | 152 B / 14 B |

## Roadmap (parity with UIFlow + M5Unit-NFC C++)

1. **NDEF** Type 2/3/4/5, then ISO-DEP/DESFire and emulation.
2. NFC-A read/write: Type 2 (Ultralight/NTAG) and MIFARE Classic (Crypto1).
3. NFC-F read/write + Japanese transport-card flow.
4. NFC-V, ISO-DEP/APDU/DESFire/Plus, NDEF.
5. NFC-A/F emulation (feasibility depends on I2C polling latency).

## References

- [M5Stack Unit NFC (U216)](https://docs.m5stack.com/en/products/sku/U216)
- [UIFlow driver](https://github.com/m5stack/uiflow-micropython/blob/master/m5stack/libs/driver/st25r3916.py)
- [M5Unit-NFC (C++)](https://github.com/m5stack/M5Unit-NFC)
- ST25R3916 datasheet / ST API

## License

MIT — see [LICENSE](LICENSE).
