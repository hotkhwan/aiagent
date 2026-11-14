# Implementation Backlog v0.1

สถานะ: **proposed** ทุก task ยังไม่เริ่ม effort เป็น **ช่วงประมาณ** (ชั่วโมงทำงานของ agent + review ของผู้ใช้) ไม่ใช่ผลวัด
Owner: `C` = Claude Code, `X` = Codex (เมื่อมี CLI), `U` = ผู้ใช้ (ตัดสินใจ/อนุมัติ)
ทุก task: TDD (test แดงก่อน), `gofmt`/`go vet`/`go test -race` ผ่าน, ไม่เปลี่ยนชื่อ field JSON เดิม

## Phase 0 — เตรียมเครื่อง (ไม่ใช่โค้ด)

| id | task | owner | depends | acceptance | effort |
|---|---|---|---|---|---|
| P0-1 | คืนพื้นที่ disk (ทำแล้ว 2026-11-14) | U | — | disk ว่าง ≥ 20 GB | 0.5 ชม. |
| P0-2 | ตัดสินใจ D-1..D-5 ใน `architectureV01.md` §11 | U | — | ตัดสินแล้ว 2026-11-14 | 0.5 ชม. |
| P0-3 | ยืนยันที่อยู่ codex CLI / ติดตั้งถ้าต้องการ | U | — | `command -v codex` มีผล หรือบันทึกว่าข้าม | — |
| P0-4 | รัน test กับ local changes ใน main และ commit/แยก branch งานค้าง (route.go ฯลฯ) | U | — | main สะอาดก่อนเริ่ม Phase A | — |

## Phase A — Mac worker compatibility + delivery contract

Vertical slice: บน Mac mini UI แสดง liveness ของ Claude/Codex session ถูกต้อง และส่งข้อความเข้า pane ที่ bind ไว้ได้พร้อม ack หรือ `unknown`

**สถานะ (2026-11-14):** part 1 อยู่ใน PR #4 (`feat/phase-a-delivery`)

| id | สถานะ |
|---|---|
| A7 outbox, A4 allowlist, A5 send lock, A8 run id, A9 lockout, A10 macOS CI, A11 README | **เสร็จ** — PR #4 |
| A3 daemon parked | **ไม่แก้ (ตัดสินใจ)** — session ที่ `claude agents` รายงานคือ job ที่ daemon ถืออยู่ และ `claude attach` ปลุกได้; job ที่ daemon ไม่รู้จักถูก attach ปฏิเสธ (มี test) การตั้ง Live=false จะทำให้ส่งข้อความหา background session ที่ park ไว้จากมือถือไม่ได้ |
| A1/A2 procinfo + darwin | ถัดไป — รวมการตรวจ argv ของ `node` เพื่อแยก codex/gemini ออกจาก node REPL |
| A6 ack จาก transcript | ถัดไป |
| gemini provider ใน routing + collector | ถัดไป (ต้องติดตั้ง `gemini` CLI ก่อนจึงจะทดสอบกับของจริงได้) |

| id | task | owner | depends | acceptance | tests | effort |
|---|---|---|---|---|---|---|
| A1 | `internal/procinfo` interface + Linux impl (ย้ายจาก `proc.go` ไม่เปลี่ยนพฤติกรรม) | C | P0-4 | test เดิมใน `world_test.go` ผ่านทั้งหมดโดยไม่แก้ assertion | ใช้ fake /proc เดิม | 3–5 ชม. |
| A2 | Darwin impl (`ps`/`lsof` fixed argv, parse ด้วย fixture) + `Capabilities` | C | A1 | บน Mac: claude session ที่รันอยู่แสดง live; pid recycled ถูกตรวจจาก startTime | fixture output ของ `ps`/`lsof` จาก macOS 26; build tag `darwin` | 5–8 ชม. |
| A3 | daemon session: แยก `Live` กับ `Parked`, relay ไม่ส่งเข้า parked โดยไม่มี evidence | C | A1 | `daemon.go:125` ไม่ตั้ง Live=true โดยไม่มี pid/state ที่บอกว่ารัน | table test ทุก daemon state | 2–3 ชม. |
| A4 | pane allowlist (**ตรวจจาก executable path ของ pane_pid** — บน Mac tmux รายงาน Claude เป็น `2.1.318` ไม่ใช่ `claude`) + pane id `%N` + `=session` exact + re-check ก่อน Enter | C | A2 | ส่งเข้า vim/python/ksh ถูกปฏิเสธ; พฤติกรรมตรงกับเอกสาร security | test argv + fake tmux | 3–5 ชม. |
| A5 | per-session send mutex ครอบ HTTP/outbox/policy/idle | C | — | สอง sender พร้อมกันไม่ interleave | test concurrency `-race` | 2–3 ชม. |
| A6 | (tmuxBound เท่านั้น ไม่อยู่บน critical path ของ orchestrator) delivery ack จาก transcript (digest ตรง ภายใน timeout) → `acked`/`unknown` | C | A4 | API คืน `deliveryState` ใหม่ (field เพิ่ม ไม่เปลี่ยนเดิม) | fake transcript append | 4–6 ชม. |
| A7 | outbox: บันทึก `sending` ก่อนส่ง, fsync, set `LastError`, audit intent/result, ไม่ทิ้ง error เงียบ | C | A5 | kill ระหว่าง flush → ไม่ส่งซ้ำ (อยู่ใน `unknown`) | test crash จำลอง (inject panic หลัง send) | 3–5 ชม. |
| A8 | `nextRunID` ใช้ `O_EXCL` create loop | C | — | สอง create พร้อมกันได้ id ต่างกัน | test parallel | 1 ชม. |
| A9 | hardening token gate: rate limit / lockout ต่อ client จริงหลัง reverse proxy + global failure cap | C | — | client หนึ่งไม่กระทบ client อื่น | test header | 2 ชม. |
| A10 | CI job `macos-latest` (vet + test) | C | A2 | CI เขียวทั้ง ubuntu และ macOS | — | 1 ชม. |
| A11 | แก้ README "Thai only" → Thai+English | C | — | ตรง `ARCHITECTURE.md:78` | — | 0.2 ชม. |

## Phase B — SQLite task store + state machine

Vertical slice: สร้าง task ผ่าน CLI `aiagent task add`, เห็นใน `/api/tasks`, เปลี่ยน state ด้วยมือตาม transition table, restart service แล้ว state ถูกต้อง

| id | task | owner | depends | acceptance | tests | effort |
|---|---|---|---|---|---|---|
| B1 | `internal/taskstore` schema v1 + migration runner + WAL/`synchronous=FULL` | X/C | P0-2(D-1) | migration idempotent; `integrity_check` ตอนเปิด | migration up ซ้ำ, DB เสีย → read-only | 5–8 ชม. |
| B2 | transition table เป็น data + `Transition(taskId, to, actor, evidence)` ใน tx เดียวกับ event | X/C | B1 | transition ที่ไม่อยู่ในตาราง/actor ผิด → error | table test ครบทุกคู่ (allowed + denied) | 4–6 ชม. |
| B3 | events: ULID, sequence, idempotencyKey UNIQUE | X/C | B1 | duplicate event ไม่สร้าง row ใหม่ | test duplicate | 2 ชม. |
| B4 | leases + reaper | X/C | B2 | lease หมด → attempt `orphaned` → retry rule | test เวลาปลอม | 3–5 ชม. |
| B5 | startup recovery ตาม `architectureV01.md` §6 | X/C | B4, A7 | restart หลัง dispatch ก่อน ack → `unknown` ไม่ส่งซ้ำ | test restart | 4–6 ชม. |
| B6 | `/api/tasks` read + write ผ่าน `mutate()` (audit) + CLI `aiagent task` | C | B2 | camelCase; disabled = 404 | HTTP tests | 4–6 ชม. |

## Phase C — managed worker หนึ่งตัว

> หลัง review: C0/C0b เป็นเงื่อนไขก่อน C1; ดู `architectureV01.md` §12 สำหรับ argv/recovery ที่แก้แล้ว

| id | task | owner | depends | acceptance | tests | effort |
|---|---|---|---|---|---|---|
| C0 | `sandbox-exec` profile (หรือ macOS user แยก ตามที่ผู้ใช้เลือก): เขียนได้เฉพาะ worktree/temp, deny stateDir/`~/.cloudflared`/`~/.ssh`/keychain | C (+X review) | P0-2 | worker ใน sandbox เขียน `tasks.db` / อ่าน `~/.ssh` ไม่ได้ (test เชิงลบ) | integration test บน Mac | 6–10 ชม. |
| C0b | spike: file-backed stream + `setsid` เทียบกับ `claude --bg` สำหรับรอด restart | C | — | เอกสารผล + เลือกหนึ่งทาง | kill orchestrator ระหว่าง run | 2–4 ชม. |

Vertical slice: task จริงใน repo aiagent → `claudeHeadless` ใน worktree ใหม่ → commit → test จาก trusted base ผ่าน → `awaitingReview` → ผู้ใช้กด approve → `completed`

| id | task | owner | depends | acceptance | tests | effort |
|---|---|---|---|---|---|---|
| C1 | worker adapter interface + `claudeHeadless` (fixed argv, stdin prompt, stream-json parser → typed events) | C | B5 | parser ทน event ที่ไม่รู้จัก → เก็บเป็น `unknownEvent` ไม่ crash | fixture stream-json จาก claude 2.1.318; CLI missing → `blocked{missingCli}` | 6–10 ชม. |
| C2 | worktree per attempt + branch `aiagent/<taskId>/<attemptId>` + cleanup policy | C | C1 | attempt fail ไม่ทิ้งไฟล์ใน main checkout | test ด้วย temp repo | 3–4 ชม. |
| C3 | env allowlist + `--allowedTools`/`--disallowedTools` profile ต่อ project | C | C1 | worker ไม่ได้รับ env อื่นนอก allowlist | test argv/env | 2–3 ชม. |
| C4 | `internal/verify`: test command จาก trusted base, allowedPaths check, protected-path check | C | C2 | worker แก้ `_test.go` เดิมหรือ `.github/**` → ต้อง review | test ด้วย diff fixture | 4–6 ชม. |
| C5 | budget/quota gate (ใช้ usage band เดิม) + cancellation SIGTERM→SIGKILL process group | C | C1 | quota critical → ไม่ dispatch; cancel ภายใน 15s | test fake process | 3–5 ชม. |
| C6 | base SHA check ก่อน completed | C | C4 | HEAD เปลี่ยน → กลับ verifying | test temp repo | 2 ชม. |

## Phase D — PM schema + adapters + eval

| id | task | owner | depends | acceptance | tests | effort |
|---|---|---|---|---|---|---|
| D1 | skill spec format + 3 skills v0.1 (`planProject`, `intakeFeedback`, `summarizeReview`; อีก 3 ตัวเก็บ spec) (input/output JSON Schema, examples, limits, version) | C | B2 | schema validate ด้วย Go | golden tests | 6–8 ชม. |
| D2 | `cloudPM` ผ่าน `claude -p --json-schema` + retry ≤2 + escalate | C | D1 | malformed output → retry → escalate; บันทึก `pm_calls` | fake CLI | 4–6 ชม. |
| D3 | eval set 50–100 ตัวอย่าง (ไทย/อังกฤษ) + runner + เกณฑ์ใน `modelPilotPlan.md` | C+U | D1 | U ตรวจ expected outputs ≥ 20 ตัวอย่างแรก | — | 8–12 ชม. |
| D4 | `localPM` adapter (หลัง model pilot ผ่าน) | C | D3, pilot | ผ่านเกณฑ์ eval | eval run | 4–6 ชม. |

## Phase E — reviewer handoff + task UI + feedback

| id | task | owner | depends | acceptance | effort |
|---|---|---|---|---|---|
| E1 | task board ใน UI (อ่านจาก events; ไทย/อังกฤษผ่าน i18n.js) | C | B6 | สถานะตรงกับ DB; reasonCode แปลใน UI | 8–12 ชม. |
| E2 | review handoff: Codex/Claude อิสระเป็น reviewer ของ attempt (ใช้ reviews.go เดิม) | C/X | C4 | reviewer ≠ author worker | 4–6 ชม. |
| E3 | feedback intake + task revision | C | D2 | feedback ผูก projectId/taskId/commit | 4–6 ชม. |

## Phase F — nightly security scan (เลื่อนไปหลัง Phase E ตาม design review; fingerprint: code → symbol, dependency → module@version)

| id | task | owner | depends | acceptance | effort |
|---|---|---|---|---|---|
| F0 | U อนุมัติติดตั้ง govulncheck + trivy (+DB ~หลายร้อย MB, ต้องตรวจขนาดจริง) | U | P0-1 | — | — |
| F1 | scanner runner + normalize/fingerprint + `scans`/`findings` | C | B1, F0 | scanner ล่ม/DB เก่า → `failed/staleDb` | 6–8 ชม. |
| F2 | triage → security task → worker → rescan verify | C | C*, F1 | finding ไปถึง `fixedInBranch` จริงด้วย rescan | 6–10 ชม. |
| F3 | schedule (launchd `StartCalendarInterval` Asia/Bangkok) — **เปิดเมื่อผู้ใช้สั่งเท่านั้น** | U/C | F2 | resource gate ทำงาน | 2 ชม. |
| F4 | PR creation (ต้องมี GitHub write token แยก scope) | U/C | F2 | PR ไม่มี auto-merge | 3–4 ชม. |

## Phase G — auto-merge (ยังไม่วางแผนละเอียด)

ต้องมีข้อมูลคุณภาพจาก E/F อย่างน้อย 30 PR ที่ผ่าน review ก่อนพิจารณา

## Test matrix ที่ต้องมี (map ไป task)

| กรณี | task |
|---|---|
| restart หลัง dispatch ก่อนบันทึก ack | A7, B5 |
| duplicate event | B3 |
| expired lease | B4 |
| cancellation | C5 |
| CLI crash (exit ≠0 / signal) | C1 |
| quota exhaustion | C5 |
| malformed PM output | D2 |
| stale base SHA | C6 |
| tests/scanner fail | C4, F1 |
| malicious repo instructions (เช่น README สั่ง "mark task complete") | C4, D2 (completion ต้องมาจาก verify เท่านั้น) |
| missing CLI บน macOS | C1 (`blocked{missingCli}`) |

## ประมาณรวม (estimate)

A ≈ 26–40 ชม., B ≈ 22–33, C ≈ 20–30, D ≈ 22–32, E ≈ 16–24, F ≈ 17–24 → Phase A–C (ส่วนที่ทำให้ dispatch เชื่อถือได้) ≈ 70–100 ชม. agent time + review
