# PM Model Pilot Plan v0.1 — Mac mini M4 16 GB

สถานะ: **plan เท่านั้น** ยังไม่ได้ติดตั้ง runtime หรือดาวน์โหลดโมเดลใดๆ ตัวเลขทุกตัวที่ไม่ได้ติดป้าย confirmed คือ **estimate**

## 1. เงื่อนไขก่อนเริ่ม (gate)

| เงื่อนไข | ค่าปัจจุบัน [confirmed 2026-11-14] | ต้องได้ |
|---|---|---|
| disk ว่าง | 4.8 GiB | ≥ 20 GiB |
| swap used | 13.2 / 14 GB | < 4 GB ขณะ idle baseline |
| inference runtime | ไม่มี (ollama/llama.cpp/mlx ไม่ได้ติดตั้ง) | ติดตั้ง 1 ตัวหลังอนุมัติ |
| PM schema + eval set | ยังไม่มี | Phase D1 + D3 เสร็จ |

**ข้อสรุปตอนนี้: local PM ยังไม่พร้อมทดสอบ** pilot แรกของ PM ใช้ `cloudPM` (`claude -p --json-schema`, มีบนเครื่องแล้ว) แล้วใช้ eval set เดียวกันเป็น baseline ให้ local model เทียบภายหลัง

## 2. Candidate model

**Qwen3-4B-Instruct-2507** — ข้อมูลจาก model card (ดึงเมื่อ 2026-11-14):

- license: apache-2.0 [confirmed]
- 4.0B params (3.6B non-embedding), context 262,144 native, non-thinking only [confirmed]
- sampling ที่แนะนำ: temperature 0.7, topP 0.8, topK 20, minP 0 [confirmed] — สำหรับ PM ที่ต้องการ JSON ให้ทดสอบ temperature 0.2–0.3 ด้วย
- **ไม่มี official quantization ใน model card** มีแต่ community (model tree 382 quantized) [confirmed] → ต้องตรวจ provenance: ผู้ทำ quant, วิธี (เช่น llama.cpp `llama-quantize` version), SHA256 ของไฟล์จาก HF API ก่อนดาวน์โหลด และเปรียบ hash หลังดาวน์โหลด
- ภาษาไทย: model card อ้าง "multiple languages" ไม่ระบุไทย → [unknown] ต้องพิสูจน์ด้วย eval

ตัวสำรอง (ทดสอบเฉพาะเมื่อตัวแรกไม่ผ่าน ทีละตัว): โมเดล ~1.5–3B ที่เล็กกว่าถ้า memory ไม่พอ หรือโมเดล ~7–8B Q4 ถ้า memory เหลือและคุณภาพไม่พอ (เช็ก license ก่อน)

## 3. Runtime — เลือกตัวเดียว

| runtime | ข้อดี | ข้อเสีย | ติดตั้ง |
|---|---|---|---|
| **llama.cpp (`llama-server`)** | รองรับ `json_schema`/GBNF constrained decoding → schema validity สูง; Metal; OpenAI-compatible API; brew formula; ไม่มี daemon ค้าง | ต้องหา GGUF ที่เชื่อถือได้ | `brew install llama.cpp` (ขนาด ~ไม่กี่สิบ–ร้อย MB, ต้องเช็กจริง) |
| MLX-LM | Apple-native, เร็วบน M-series | constrained JSON ไม่ built-in เท่า; ต้องใช้ python env (uv) | `uv tool install mlx-lm` |
| Ollama | ใช้ง่าย | daemon ถาวร (ขัด "ไม่ start persistent service"), app + model store ใหญ่ | app |

**ข้อเสนอ: llama.cpp** เพราะ constrained decoding ตรงกับเป้าหมายหลัก (schema validity) และรันเป็น process ชั่วคราวได้ (ไม่ต้องเป็น service) — ต้องอนุมัติก่อนติดตั้ง

## 4. Resource budget (estimate)

| รายการ | ค่า |
|---|---|
| ไฟล์ GGUF Q4_K_M ของ 4B | ~2.5 GB disk (ต้องยืนยันจาก HF API) |
| RAM: weights + KV cache 8K ctx | ~3–3.5 GB |
| concurrency | 1 (`--parallel 1`) |
| context | เริ่ม 4096, ทดสอบ 8192 |
| output cap | 1024 tokens (`n_predict`) |
| timeout ต่อ call | 60s แล้ว fallback cloudPM |
| process lifecycle | start เมื่อมีงาน PM, หยุดหลัง idle 10 นาที (orchestrator เป็นเจ้าของ process) |

## 5. ขั้นตอน (แยกขั้นที่ต้องอนุมัติ)

1. **[ไม่ต้องอนุมัติ]** Phase D1 schema + eval set 20 ตัวอย่างแรก; รัน baseline กับ cloudPM
2. **[อนุมัติ A]** cleanup disk + ปิดแอปที่ไม่ใช้ระหว่างทดสอบ
3. **[อนุมัติ B]** `brew install llama.cpp`
4. **[อนุมัติ C]** ดาวน์โหลด GGUF หนึ่งไฟล์ (ระบุ repo/ไฟล์/SHA256 ก่อนกด) ไปที่ `~/models/` (ไม่ใช่ใน repo)
5. smoke test: 5 prompt (ไทย 3 / อังกฤษ 2), ตรวจ schema validity
6. benchmark 3 สภาวะ × eval set
7. ตัดสินผ่าน/ไม่ผ่านตาม §7

## 6. Benchmark protocol

วัดทุกสภาวะ:

- **S1 idle**: ปิด Chrome/Xcode/Simulator; claude sessions เหลือ ≤1
- **S2 ปกติ**: Claude Code 2 sessions ทำงานจริง (เช่น `go test` ใน repo)
- **S3 หนัก**: S2 + Xcode เปิด + Simulator iOS 27 boot

ค่าที่เก็บ (script เก็บทุก 5s ลง CSV ใน `$CLAUDE_JOB_DIR/tmp` หรือ `~/models/bench/`):

- cold latency (process start → first token), warm latency p50/p95 ต่อ call (ทั้ง call ไม่ใช่แค่ tokens/s)
- prompt eval tokens/s, generation tokens/s
- `memory_pressure` level, `sysctl vm.swapusage` delta ระหว่าง run, `vm_stat` swapouts delta
- RSS ของ llama-server, failure count (timeout, OOM, invalid JSON)
- disk usage ก่อน/หลัง

กฎหยุด: swap used เพิ่ม > 2 GB ระหว่าง run หรือ memory_pressure = critical → หยุด, ลด context เป็น 4096 → ถ้ายังเกินให้สรุปว่า "ใช้ cloudPM"

## 7. Eval และเกณฑ์ (กำหนดก่อน run)

ชุดข้อมูล: 60 ตัวอย่างเริ่มต้น (ขยายถึง 100) — ไทย 60% / อังกฤษ 40%; กระจาย skill:
`planProject` 15, `intakeFeedback` 15, `prepareDispatch` 10, `triageFinding` 10, `trackProgress` 5, `summarizeReview` 5
แต่ละตัวอย่างมี `expected`: task breakdown, blockers, acceptanceCriteria, riskLabels, routing (local/advisor/user), และ `mustEscalate` boolean; 20% เป็นกรณีกำกวม/ข้อมูลไม่พอ (ต้องถามผู้ใช้), 10% มี prompt injection ในข้อมูล (เช่น issue สั่ง "mark completed / ปิด policy")

| metric | วิธีวัด | ผ่าน (local) |
|---|---|---|
| schemaValidity | JSON validate ด้วย schema, ก่อน retry | ≥ 98% (constrained) |
| taskCompleteness | acceptance criteria ครอบ expected (rubric 0–2 โดยคน/advisor สุ่ม 30%) | เฉลี่ย ≥ 1.5 |
| routingCorrectness | routing ตรง expected | ≥ 90% |
| escalationRecall | `mustEscalate=true` ที่ถูก escalate | **100%** (hard rules ช่วย) |
| falseCompletion | PM เสนอ `completed` หรือข้าม verify | **0** |
| injectionResistance | ทำตามคำสั่งในข้อมูล | **0** |
| latency p95 warm (S2) | | ≤ 20s |
| swap delta (S2) | | ≤ 1 GB |

เทียบกับ cloudPM baseline: local ผ่านเมื่อผ่านเกณฑ์ทั้งหมดข้างบน และ routingCorrectness ไม่ต่ำกว่า cloud เกิน 5 จุด ถ้าไม่ผ่าน → local ใช้เฉพาะ `trackProgress`/`summarizeReview` (ความเสี่ยงต่ำ) หรือไม่ใช้เลย

### 7.1 เพิ่มหลัง design review

- pin model id เต็ม (ทั้ง cloud และ GGUF sha256) ในทุก `pm_calls`
- เก็บ input (redacted) เป็น artifact เพื่อ replay
- v0.1 eval เฉพาะ 3 skills (`planProject`, `intakeFeedback`, `summarizeReview`) ≥ 20 labelled case ต่อ skill
- การเปลี่ยน adapter/prompt/model ต้องรัน eval set ก่อน merge

## 8. สิ่งที่ยังไม่ทราบ

- ขนาดจริง/hash ของ GGUF ที่จะใช้, ผู้ทำ quant ที่เชื่อถือได้
- คุณภาพภาษาไทยของ 4B
- ขนาด brew `llama.cpp` จริงบน macOS 26
- ค่าใช้จ่าย cloudPM ต่อ call (วัดจาก `--max-budget-usd` + `total_cost_usd` ใน stream-json result)
