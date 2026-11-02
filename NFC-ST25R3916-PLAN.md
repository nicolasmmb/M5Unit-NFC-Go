# Unit NFC (ST25R3916) — Plano, status e melhorias

> Documento vivo do épico **M5Unit-NFC-Go** + POC `poc/nfc`.
> Atualizado em 2026-11-01.

## 1. Objetivo

Driver **100% Go** para o **M5Stack Unit NFC (SKU U216, ST25R3916)** com
paridade com:

- **UIFlow** (`unit/nfc.py`, `driver/st25r3916.py`)
- **M5Unit-NFC C/C++** (NFC-A/B/F/V, ISO-DEP, NDEF, emulação)

Sem C, C++, CGo ou Arduino. Cada etapa com testes, benchmark, validação de
hardware e aprovação antes da próxima.

## 2. Hardware e conexão

| Item | Valor |
|---|---|
| Unit | M5Stack Unit NFC (U216) |
| Chip | ST25R3916 (13,56 MHz) |
| Interface | I2C **0x50**, 400 kHz |
| Grove (M5StickC Plus2) | **I2C1: SDA=G32, SCL=G33** |
| IRQ | não exposto no Grove → driver faz polling |
| Protocolos | NFC-A/B/F/V, emulação NFC-A/F |

O Grove é compartilhado com Encoder/GPS/Fingerprint2/ToF/Joystick2: **um unit
por vez**.

## 3. Repositórios e versões

| Repo | Conteúdo | Última tag |
|---|---|---|
| [M5Unit-NFC-Go](https://github.com/nicolasmmb/M5Unit-NFC-Go) | driver puro Go | **v0.8.1** |
| `m5stick-obd2-cnx` | POC `poc/nfc` | commit **7c9b010** |

Tags da lib: `v0.1.0` (NFC-A), `v0.2.0/0.2.1` (NFC-F), `v0.3.0/0.3.1`
(NFC-B + docs), `v0.4.0` (NFC-V), `v0.5.0` (Type 2/Classic read),
`v0.6.x` (writes, correções), `v0.7.0` (FeliCa system codes/read),
`v0.8.0/0.8.1` (emulação FeliCa).

## 4. Arquitetura

```
M5Unit-NFC-Go/
├── st25r3916.go   # transporte I2C, espaço-B 16 bits, init, FIFO, timers, IRQ
├── nfca.go        # REQA/WUPA, anticolisão, SELECT, HALT, Card, TypeName
├── nfca/type2     # READ 0x30 / WRITE 0xA2 (em classic.go)
├── crypto1.go     # Crypto1 (LFSR), CRC-A, paridade
├── classic.go     # auth, READ/WRITE cifrados, verificação
├── felica.go      # polling, system codes, tipo, Read Without Encryption
├── emulation_f.go # target FeliCa: PT memory + TSN + eventos RF
├── nfcv.go        # ISO15693: VCD 1-of-4, CRC, Manchester, inventory
├── device_test.go # emulador ST25R3916 + PICC roteirizado
└── bench_test.go  # benchmarks com tx/op e allocs
```

POC `poc/nfc`:

```
A → F → B → V → T2 → CL → EMU → INFO
```

- Cada tela configura **somente** o seu protocolo.
- **A**: RF on/off.
- **B**: reinicia o chip (ou arma/grava nas telas T2/CL — 2 toques).
- **C**: próxima tela (sem toque longo; C longo desliga o Stick).

## 5. Passos concluídos

### 5.1 Núcleo ST25R3916 (H1) ✅
- Transporte I2C, registradores 8 bits e espaço-B 16 bits.
- Init completo: identidade, SET_DEFAULT, TEST_ACCESS, IO/TX, field detector,
  NFCIP, antenna tuning, oscilador, reguladores.
- FIFO, timers NRT/FWT, máscaras/interrupções, `context` e timeouts.
- **0 alocações** em `Begin`/detecções.

### 5.2 Probe (H2) ✅
- **NFC-A**: REQA/WUPA, anticolisão CL1/CL2, SELECT, UID 4/7, ATQA/SAK,
  tipo e memória.
- **NFC-F**: polling 212 kbps, IDm/PMm, **system codes**, tipo
  Standard/Lite-Lite-S/Plug.
- **NFC-B**: REQB/ATQB → PUPI/APPL/PROTO.
- **NFC-V**: ISO15693 inventory → UID/DSFID.
- POC com tela dedicada por protocolo.

### 5.3 Type 2 (H3) ✅ lib / ⏳ hardware
- READ 0x30 (quad/página), WRITE 0xA2 + ACK + verificação.
- Sem cartão NTAG/Ultralight para validar no hardware.

### 5.4 MIFARE Classic (H4) ✅ leitura / ⏳ escrita
- **Crypto1** validado com vetor Proxmark.
- Auth chave A, READ cifrado, CRC.
- WRITE implementado (auth bloco/trailer, pre-read, ACK, read-back).
- **Leitura validada**: UID `0A EE 8D 02`, bloco 1 lido.
- **Escrita ainda não confirmada** no hardware (ver §7).

### 5.5 FeliCa (H5) ✅ detect/read API
- Polling, IDm/PMm.
- Request System Code (0x0C) → lista de system codes.
- Tipo genérico: Standard/Lite/Lite-S/Plug.
- `FeliCaReadBlock` (Read Without Encryption, 16 B) implementado.
- Hardware: **FeliCa Standard**, 2 system codes, ~19 ms.

### 5.6 Emulação NFC-F (H9 parcial) ✅ básica
- Target FeliCa passivo via **PT memory F** + **TSN**.
- System code wildcard `0xFFFF`, IDm/PMm configuráveis.
- Responde ao **Polling/SENSF**.
- **Validado**: Flipper Zero identificou o cartão emulado como FeliCa.
- Limitação: apenas polling; não responde a serviços/leitura/escrita.

### 5.7 Performance e qualidade ✅
- Benchmarks com `tx/op`, allocs e footprint.
- Contratos: 0 allocs no núcleo, `Device` ~152 B, `Card` 14 B.
- CI verde, MIT, testes com `-race`.

Números atuais (host, Apple M2 Pro):

| Benchmark | Resultado |
|---|---:|
| `Begin` | 22,7 ms / 64 tx |
| `Detect` (realista) | 13,9 ms / 51 tx |
| `Detect` sem cartão | 25,2 ms / 72 tx |
| `DetectF` | 62 µs / 17 tx |
| `DetectB` | 61,5 µs / 17 tx |
| `DetectV` | 61,8 µs / 15 tx |
| `DetectFInfo` | 122,7 µs / 34 tx |
| `Type2ReadPage` | 1,23 ms / 20 tx |
| Alocações | **0** em todas as operações |
| `Device` / `Card` | **152 B / 14 B** |

## 6. Estado atual por item

| Item | Lib | Hardware |
|---|---|---|
| NFC-A detect/identify | ✅ | ✅ |
| NFC-F detect/system codes | ✅ | ✅ |
| NFC-B detect | ✅ | ⏳ tag |
| NFC-V detect | ✅ | ⏳ tag |
| Type 2 read | ✅ | ⏳ tag |
| Type 2 write | ✅ host | ⏳ tag |
| Classic read | ✅ | ✅ |
| Classic write | ✅ lib | ⏳ falha em validação |
| FeliCa read block | ✅ API | ⏳ service code |
| Emulação FeliCa polling | ✅ | ✅ Flipper |
| NDEF | ❌ | — |
| ISO-DEP/APDU/DESFire | ❌ | — |
| Emulação completa FeliCa | ❌ | — |
| Emulação NFC-A | ❌ | — |

## 7. O que está faltando

### 7.1 Curto prazo (fechar o que está aberto)
- [ ] **Classic write**: capturar a fase exata do erro
  (`ErrAuth`/`ErrRead`/`ErrNotAck`/`ErrVerify`) e corrigir.
  - Hipóteses: ACK Crypto1, auth no trailer vs bloco, timing, chave B.
- [ ] **Type 2** read/write no hardware (precisa NTAG/Ultralight).
- [ ] **NFC-B** com tag ISO14443-B.
- [ ] **NFC-V** com tag ISO15693 (ICODE/Tag-it/ST25DV).
- [ ] Confirmar o IDm emulado no Flipper (`01 2E 50 E5 3C 4B 4F 29`).

### 7.2 FeliCa
- [ ] Request Service (0x02) para listar serviços/chaves.
- [ ] Request Response (0x04) para modo do Standard.
- [ ] Leitura de múltiplos blocos (block list).
- [ ] Distinguir **Lite** de **Lite-S**.
- [ ] Write Without Encryption (0x08).
- [ ] Lite/Lite-S autenticado (AES/3DES, MAC, WCNT).
- [ ] NDEF Type 3.
- [ ] Fluxo de cartão de transporte (histórico/saldo) por service code.

### 7.3 Emulação
- [ ] Resposta por software ao Polling (se necessário além do PT memory).
- [ ] Request Service / Request Response emulados.
- [ ] Read/Write Without Encryption emulados (memória).
- [ ] Emulação Lite-S (MAC/WCNT).
- [ ] Emulação NFC-A (UID, anticolisão, READ/WRITE).
- [ ] Medir latência I2C e viabilidade de emulação completa.

### 7.4 Demais protocolos
- [ ] **NFC-B ISO-DEP/APDU**.
- [ ] **DESFire/Plus/ST25TA** + file systems (AES/CMAC).
- [ ] **NFC-V read/write/dump**.
- [ ] **NDEF Type 2/3/4/5** (leitura e escrita).
- [ ] **MIFARE Plus SL0/SL1/SL3** e value blocks.
- [ ] Release **v1.0.0** com docs completos.

## 8. Sessão de melhorias

### 8.1 Confiabilidade e depuração
| Melhoria | Motivo | Prioridade |
|---|---|---|
| Log de fase na escrita Classic (auth/read/ack/verify) no serial | isolar a falha atual | **P0** |
| Tentar key B além da key A | cartões com chave B | P1 |
| Repetir auth/write com re-select + HLTA | cartões instáveis | P1 |
| Watchdog/timeout global por operação | evitar tela travada | P1 |
| Teste de regressão com cartão real (script de bancada) | validar cada release | P1 |

### 8.2 Emulação
| Melhoria | Motivo | Prioridade |
|---|---|---|
| Responder ao Polling por software | independência do PT memory | **P0** |
| Emular Request Service/Read/Write | paridade C++ | P1 |
| Medir latência I2C (IRQ→FIFO→TX) | viabilidade da emulação completa | P0 |
| Suportar 212/424 kbps | leitores diferentes | P2 |
| Emulação Lite-S (MAC/WCNT) | paridade total | P2 |

### 8.3 Performance
| Melhoria | Motivo |
|---|---|
| Cachear configuração de timing/auth | menos leituras I2C |
| Reduzir transações no polling (ler IRQ+FIFO juntos) | emulação e detecção |
| Flush parcial de tela no POC | UI mais fluida |
| Buffer de paridade 32 B e checagens | já corrigido (v0.6.2) |
| Benchmarks de escrita Classic/FeliCa | medir regressões |
| Contratos de alloc por protocolo | manter 0 allocs |

### 8.4 API e testes
| Melhoria | Motivo |
|---|---|
| `context` em todas as operações de rádio | cancelamento uniforme |
| Tipos fortes para UID/IDm (evitar slices) | segurança/0 alloc |
| Erros tipados por protocolo | diagnóstico |
| Emulador de PICC Classic com Crypto1 (tag-side) | testar escrita no host |
| Emulador de leitor FeliCa para emulação | testar emulação no host |
| Testes de NFC-B/V com tags roteirizadas | cobertura |
| CI com `-race` + limites de regressão de bench | qualidade |

### 8.5 Segurança e uso
| Melhoria | Motivo |
|---|---|
| Whitelist de blocos/páginas na escrita | nunca UID/lock/trailer |
| Confirmação em duas etapas (B 2×) | já implementado |
| Aviso de flash/desgaste | calibração/deadzone |
| Evitar C longo (desliga o Stick) | já contornado |
| Documentar que A/B não são legíveis por polling neste board | evitar regressões de UI |

### 8.6 Documentação
- Exemplo por protocolo (A/F/B/V/T2/CL/EMU).
- Matriz de hardware validado por cartão.
- Guia de service codes FeliCa.
- Notas de limitações (I2C, emulação, Plus SL3).
- `BENCHMARKS.md` com antes/depois por otimização.

## 9. Ordem recomendada

1. **Fechar Classic write** (P0).
2. **Emulação FeliCa completa** (polling por software + serviços/read).
3. **NDEF Type 2/3/4/5**.
4. **ISO-DEP/APDU/DESFire/Plus**.
5. **NFC-V read/write** e emulação NFC-A.
6. **Release v1.0** e documentação final.

## 10. Definition of Done (global)

1. Go puro, sem CGo; `go vet` e `go test -race` verdes.
2. Testes de host com emulador + vetores oficiais.
3. 0 alocações no núcleo + orçamento de RAM.
4. Benchmark com `tx/op`, tempo e memória; antes/depois.
5. Validação no hardware com log serial.
6. README/API/BENCHMARKS atualizados.
7. Commit `noreply` + tag; CI verde.
8. Sem regressão nos testes anteriores.

## 11. Matriz de hardware

| Cartão/tag | Protocolo | Status |
|---|---|---|
| ISO14443-4 (UID 4/7, SAK 0x20) | NFC-A | ✅ detectado |
| FeliCa Standard | NFC-F | ✅ IDm/PMm + 2 system codes |
| MIFARE Classic `0A EE 8D 02` | NFC-A | ✅ leitura / ⏳ escrita |
| NTAG/Ultralight | NFC-A Type 2 | ⏳ sem tag |
| ISO14443-B | NFC-B | ⏳ sem tag |
| ISO15693 (ICODE etc.) | NFC-V | ⏳ sem tag |
| Flipper Zero (leitor) | NFC-F | ✅ viu o target emulado |

## 12. Comandos úteis

```bash
# POC
make nfc                 # flash + monitor
make build               # compila todos os POCs
make check               # gopls check

# Lib
cd ../M5Unit-NFC-Go
make test                # go test -race
make bench               # benchmarks com tx/op
make alloc               # contratos de 0 allocs
make bench-save NAME=before
make bench-compare FROM=before TO=after
```

## 13. Limitações e riscos conhecidos

- **Sem IRQ no Grove**: todo o timing depende de polling I2C; emulação
  completa pode ser limitada.
- **C longo desliga o M5Stick** neste board; usar B 2× para ações.
- **A/B não são legíveis por `Get()`** (só interrupção); C é legível.
- **MIFARE Plus SL3** tem problema documentado na versão I2C da própria
  M5Stack.
- **Tags 125 kHz** não são suportadas pelo ST25R3916.
- Escrita Classic ainda em depuração; não usar em cartões de acesso.
- Emulação atual só responde ao polling.

## 14. Bases de referência (libs e documentos originais)

### 14.1 M5Stack — produto e documentos

| Fonte | Uso |
|---|---|
| <https://docs.m5stack.com/en/products/sku/U216> | página oficial do Unit NFC (U216) |
| <https://shop.m5stack.com/products/nfc-unit-st25r3916> | especificações, fotos e documentos |
| <https://docs.m5stack.com/en/unit/Unit_NFC> | documentação do unit (quando publicada) |

### 14.2 UIFlow (MicroPython) — referência de paridade NFC-A

Repositório: <https://github.com/m5stack/uiflow-micropython>

| Arquivo | Uso no nosso port |
|---|---|
| `m5stack/libs/unit/nfc.py` | classe `NFCUnit` (wrapper do unit) |
| `m5stack/libs/driver/nfc.py` | `NFCReader`, `Card`, tabelas de tipo/memória, identificação |
| `m5stack/libs/driver/st25r3916.py` | init, configuração NFC-A, anticolisão, Crypto1 auth/read/write |
| `m5stack/libs/driver/mifare_classic1.py` | Crypto1, CRC-A, paridade, `suc_23` |
| `docs/source/unit/nfc.rst` | API documentada (detect/read/write/halt/rf_on/off) |
| `examples/unit/nfc/cores3_unit_nfc_example.py` | fluxo de exemplo (detect/read/write) |

**Limite importante:** a UIFlow implementa **somente NFC-A**. Não há NFC-F,
NFC-B, NFC-V, NDEF, ISO-DEP nem emulação.

### 14.3 M5Unit-NFC (C/C++) — referência de paridade completa

Repositório: <https://github.com/m5stack/M5Unit-NFC>

Dependências: [M5UnitUnified](https://github.com/m5stack/M5UnitUnified),
[M5Utility](https://github.com/m5stack/M5Utility),
[M5HAL](https://github.com/m5stack/M5HAL).

| Arquivo | Uso no nosso port |
|---|---|
| `README.md` | matriz de PICCs suportados, emulação, limitações (I2C/Plus SL3) |
| `src/unit/unit_ST25R3916.cpp/.hpp` | transporte, init, PT memory, timers, IRQs |
| `src/unit/unit_ST25R3916_nfca.cpp` | configuração NFC-A |
| `src/unit/unit_ST25R3916_nfcb.cpp` | configuração NFC-B (tabela legada usada no port) |
| `src/unit/unit_ST25R3916_nfcf.cpp` | configuração FeliCa + emulação |
| `src/unit/unit_ST25R3916_nfcv.cpp` | configuração ISO15693 |
| `src/nfc/layer/a/nfc_layer_a.cpp` | detecção, read/write NFC-A |
| `src/nfc/layer/f/nfc_layer_f.cpp` | FeliCa detect/system codes/read/write |
| `src/nfc/layer/f/emulation_layer_f.cpp` + `_ST25R3916.cpp` | emulação NFC-F (PT memory, TSN, polling) |
| `src/nfc/layer/v/nfc_layer_v.cpp` | ISO15693 detect/read/write |
| `src/nfc/layer/b/nfc_layer_b.cpp` | NFC-B REQB/ATQB, ISO-DEP |
| `src/nfc/crypto/mifare_classic_crypto1.hpp` | Crypto1 |
| `src/nfc/ndef/*` | NDEF/TLV/records |
| `src/nfc/isoDEP/*` | APDU/DESFire/file systems |
| `test/mifare_classic_crypto1_test.cpp` | vetor Proxmark (usado no teste da nossa lib) |
| `examples/UnitUnified/NFCA|NFCB|NFCF|NFCV` | detect/dump/read-write/emulation |

**Observação de hardware:** a README da C++ alerta que I2C lento (especialmente
SoftwareI2C) inviabiliza o timing de emulação. O Unit NFC usa I2C de hardware
no StickC; a emulação básica foi validada, a completa precisa de medição.

### 14.4 STMicroelectronics e normas

| Fonte | Uso |
|---|---|
| [ST25R3916 product page](https://www.st.com/en/imaging-and-photonics-solutions/st25r3916.html) | datasheet, reference manual e API |
| Datasheet **DS12484** | registradores, timers, FIFO, PT memory, target mode |
| ST RFAL / ST25R3916 API | referência de sequências (a C++ usa um port) |
| ISO/IEC 14443-3/4 | NFC-A/B, anticolisão, ISO-DEP |
| ISO/IEC 15693 | NFC-V (inventory, read/write) |
| ISO/IEC 18092 (NFC-F) / FeliCa | polling, SENSF, serviços/blocos |
| NFC Forum NDEF/TLV | Type 2/3/4/5 |
| Proxmark3 test vector | validação do Crypto1 |

## 15. Matriz de comparação com as libs originais

Legenda: ✅ implementado/validado · 🟡 implementado, validação pendente ·
❌ não implementado · — não se aplica.

| Recurso | UIFlow (Py) | M5Unit-NFC (C/C++) | M5Unit-NFC-Go (nosso) |
|---|---|---|---|
| Init ST25R3916 (I2C) | ✅ | ✅ | ✅ |
| NFC-A detect/UID/ATQA/SAK | ✅ | ✅ | ✅ (HW) |
| NFC-A Type 2 read | ✅ | ✅ | ✅ (host) |
| NFC-A Type 2 write | ✅ | ✅ | 🟡 (host; sem tag) |
| MIFARE Classic auth/read | ✅ | ✅ | ✅ (HW) |
| MIFARE Classic write | ✅ | ✅ | 🟡 (HW pendente) |
| NFC-B detect/ATQB | ❌ | ✅ | ✅ (lib; sem tag) |
| NFC-B ISO-DEP/APDU | ❌ | ✅ | ❌ |
| NFC-F detect/IDm/PMm | ❌ | ✅ | ✅ (HW) |
| NFC-F system codes/tipo | ❌ | ✅ | ✅ (HW) |
| NFC-F Read Without Encryption | ❌ | ✅ | ✅ (API) |
| NFC-F Write / Lite-S auth | ❌ | ✅ | ❌ |
| NFC-V detect/UID/DSFID | ❌ | ✅ | ✅ (lib; sem tag) |
| NFC-V read/write | ❌ | ✅ | ❌ |
| NDEF Type 2/3/4/5 | ❌ | ✅ | ❌ |
| Emulação NFC-A | ❌ | ✅ | ❌ |
| Emulação NFC-F (polling) | ❌ | ✅ | ✅ (HW/Flipper) |
| Emulação NFC-F completa | ❌ | ✅ | ❌ |
| DESFire/Plus/ST25TA/value blocks | ❌ | ✅ | ❌ |
| Transporte SPI | ❌ | ✅ | ❌ (U216 é I2C) |
| 0 alocações por operação | — | — | ✅ |
| `context`/timeouts | parcial | ✅ | ✅ |
| Leitura de 4/7 bytes UID e cascade | ✅ | ✅ | ✅ |
| Anticolisão CL1/CL2 | ✅ | ✅ | ✅ |
| HLTA / RF on/off | ✅ | ✅ | ✅ |
| Tabela de tipos e memória | ✅ | ✅ | parcial (A/F) |

### 15.1 Diferenças intencionais

| Item | Original | Nosso |
|---|---|---|
| UIFlow: `fill_color` do LED | brilho aplicado 2× (bug) | aplicado 1× (Joystick2, outro unit) |
| Endereço/limites | alguns sem validação | validados e com erros tipados |
| NFC-F emulação | software/estado completo | polling via PT memory + TSN (básico) |
| I2C | UIFlow 400 kHz; C++ 400 kHz | 400 kHz; sem IRQ no Grove |
| Escrita | sem gate | gate B 2× + whitelist de bloco/página |

### 15.2 Mapa de port (o que veio de onde)

| Nosso arquivo | Base principal |
|---|---|
| `st25r3916.go` | `unit_ST25R3916*.cpp` + `st25r3916.py` |
| `nfca.go` | `nfc_layer_a.cpp` + `nfc.py` |
| `crypto1.go` / `classic.go` | `mifare_classic1.py` + `mifare_classic_crypto1.hpp` + vetor Proxmark |
| `type2` (em `classic.go`) | `nfc.py` (`type2_read_quad/write_page`) |
| `nfcv.go` | `nfcv.cpp` + `unit_ST25R3916_nfcv.cpp` |
| `nfcb` (em `nfca.go`/`st25r3916.go`) | `unit_ST25R3916_nfcb.cpp` (tabela legada) |
| `felica.go` | `nfc_layer_f.cpp` + `nfcf.cpp` |
| `emulation_f.go` | `emulation_layer_f.cpp` + `_ST25R3916.cpp` + `nfcf.cpp` |
| Tabelas de tipo/memória | `nfc.py` (`_TYPE_NAMES`, `_USER_MEMORY`) |

## 16. Como comparar/reproduzir

1. Abra os arquivos das libs originais nos links acima.
2. Compare a sequência de init com `st25r3916.go` (ordem de registradores).
3. Rode os testes da nossa lib:
   ```bash
   cd ../M5Unit-NFC-Go
   make test && make alloc && make bench
   ```
4. Para cada protocolo, use a tela correspondente do POC e compare com o
   exemplo C++ equivalente (`examples/UnitUnified/NFCx/...`).
5. Validação de hardware: anote o cartão, UID/IDm, tipo, system codes e o
   resultado na §11.

> As referências acima são as versões `master`/`main` consultadas em
> **2026-11-01**; ao atualizar a lib original, revisar as sequências de init e
> emulação.
