# Architecture v0.1 — aiagent PM / Orchestrator

สถานะ: **proposed** (ยังไม่มีโค้ด) อ้างหลักฐานจาก machine/repo audit ภายใน (ไม่เผยแพร่)
ขอบเขต: single-host pilot บน Mac mini (M4 16 GB) ผู้ใช้หนึ่งคน

## 1. หลักการ

1. **PM เสนอ, Go ตัดสิน** — model ใดๆ (local/cloud) คืนได้แค่ JSON ตาม schema; การเปลี่ยน state, dispatch, merge ทำโดย Go orchestrator ตาม rule เท่านั้น
2. **ไม่อ้าง exactly-once** — delivery มี state `unknown` เสมอ และต้อง reconcile ก่อนส่งซ้ำ
3. **Unknown is a value** (กฎเดิมใน `docs/ARCHITECTURE.md`) ขยายไปถึง status ของ session, liveness บน macOS, scan result
4. **No shell, fixed argv** (กฎเดิม) ใช้กับ worker adapter, scanner, git ทุกตัว
5. **ข้อมูลจาก repo/issue/transcript/scanner = untrusted** ห้ามเปลี่ยน policy หรือสิทธิ์จากเนื้อหาเหล่านี้
6. **เพิ่มเฉพาะที่จำเป็น** — SQLite เดียว ไม่มี Kafka/Redis/vector DB/framework ใหม่

## 2. Components

```
browser / phone ──(Cloudflare tunnel)──▶ cmd/aiagent serve
                                                   │
       ┌───────────────────────────────────────────┼─────────────────────────────┐
       │ internal/world (เดิม: collector, http, relay, audit)                     │
       │      ▲ อ่าน snapshot                         │ /api/tasks/* (ใหม่)          │
       │      │                                       ▼                             │
       │ internal/procinfo ◀── internal/orchestrator ──▶ internal/taskstore (SQLite) │
       │  (linux + darwin)        │  state machine, scheduler,                      │
       │                          │  leases, policy gate, recovery                  │
       │                          ├──▶ internal/planner   (PM adapters: local/cloud) │
       │                          ├──▶ internal/worker    (claudeHeadless, codexExec, │
       │                          │                        tmuxBound[observe/bound])│
       │                          ├──▶ internal/verify    (tests, scanners, review)  │
       │                          └──▶ internal/feedback                             │
       └────────────────────────────────────────────────────────────────────────────┘
```

| package | หน้าที่ | หมายเหตุ |
|---|---|---|
| `internal/procinfo` | `Process{pid, ppid, startTime, comm, cwd}` + `Capabilities{liveness, ancestry, cwd, openFiles}` | Linux = `/proc` (ย้ายจาก `proc.go`), Darwin = `ps -axo pid=,ppid=,lstart=,comm=` fixed argv + `lsof -a -p PID -d cwd -Fn` (หรือ libproc ภายหลัง) ถ้าทำไม่ได้ให้คืน capability `unsupported` ห้ามคืน "live" |
| `internal/taskstore` | SQLite schema + migrations + repository | ไฟล์ `<stateDir>/tasks.db`, WAL, `synchronous=FULL` |
| `internal/orchestrator` | transitions, scheduler tick, lease reaper, reconciliation | เป็น writer เดียวของ taskstore |
| `internal/worker` | adapter interface + implementations | ดู §5 |
| `internal/planner` | PM skills: เรียก model, validate schema, retry ≤2, escalate | ไม่มีสิทธิ์ shell/git/token |
| `internal/verify` | รัน test command ที่ config กำหนด, scanner, diff policy check, independent review | test command มาจาก trusted base ไม่ใช่จาก branch ของ worker |
| `internal/feedback` | intake → feedback record → task revision | |

`internal/world` เหลือหน้าที่ monitor + HTTP; outbox/policy ticker ย้ายการส่งไปผ่าน orchestrator (ดู §7)

**การตัดสินใจที่ต้องให้ผู้ใช้รับรอง (D-1):** SQLite driver ต้องเพิ่ม dependency หนึ่งตัว เสนอ `modernc.org/sqlite` (pure Go ไม่ต้อง cgo, cross-compile ง่าย) เทียบกับ `mattn/go-sqlite3` (cgo, เร็วกว่า) — แนะนำ modernc เพราะ CI/packaging ปัจจุบันไม่มี cgo

## 3. Data model (SQLite)

ทุก JSON column/key เป็น camelCase; ตาราง/column ใช้ snake_case ใน SQL แต่ API ส่งออก camelCase

```
projects(projectId PK, name, repoPath, defaultBranch, allowedPaths JSON, testCommand JSON(argv),
         policyVersion, createdAt)
tasks(taskId PK, projectId FK, taskRevision INT, title, kind[bug|feature|ux|regression|security|chore],
      status, priority, acceptanceCriteria JSON, allowedPaths JSON, blockedBy JSON[taskId],
      budget JSON{maxUsd,maxMinutes,maxAttempts}, deadlineAt, riskLabels JSON, createdBy, updatedAt)
task_revisions(taskId, taskRevision, diff JSON, reason, actor, at)          -- rework/scope change
attempts(attemptId PK, taskId, taskRevision, runId, workerId, sessionId, baseCommit, branch,
         worktreePath, status, leaseExpiresAt, heartbeatAt, startedAt, endedAt, exitReason,
         costUsd, policyVersion)
events(eventId PK(ULID), sequence INT AUTOINCREMENT UNIQUE, at, actor, taskId, attemptId,
       type, payload JSON, idempotencyKey UNIQUE NULL)
deliveries(deliveryId PK, attemptId, target, payloadDigest, state[pending|sending|sent|acked|unknown|failed|cancelled],
           tries, lastError, createdAt, sentAt, ackedAt, expiresAt)
artifacts(artifactId PK, attemptId, kind[patch|testLog|scanReport|review|screenshot], path, sha256, bytes)
findings(findingId PK, scanId, findingFingerprint UNIQUE(projectId,..), tool, toolVersion, databaseTimestamp,
         severity, location, reachabilityEvidence, state[open|triaged|fixedInBranch|merged|deployed|verified|wontFix|falsePositive],
         taskId NULL, patchCommit NULL, verificationArtifacts JSON)
scans(scanId PK, projectId, baseCommit, tool, toolVersion, databaseTimestamp, status[ok|failed|staleDb], startedAt, endedAt)
feedback(feedbackId PK, projectId, taskId NULL, commit NULL, artifactId NULL, kind, expected, actual,
         attachments JSON, status, linkedTaskId NULL, at)
pm_calls(callId PK, skill, skillVersion, model, inputDigest, outputValid BOOL, retries, escalatedTo, latencyMs, at)
```

กฎ: ทุกการเปลี่ยน `tasks.status`/`attempts.status` เกิดใน transaction เดียวกับการ insert `events` (event = source of truth, row = projection)

## 4. Task lifecycle

```
queued ──▶ ready ──▶ running ──▶ verifying ──▶ awaitingReview ──▶ completed
   │         │          │            │               │
   └────┬────┴────┬─────┴─────┬──────┴───────┬───────┘
        ▼         ▼           ▼              ▼
     blocked    failed     cancelled     (rework → queued, taskRevision+1)
```

| from → to | actor | เงื่อนไข / หลักฐานที่ต้องมี |
|---|---|---|
| queued → ready | orchestrator | `blockedBy` ทั้งหมด `completed`, acceptanceCriteria ไม่ว่าง, project อยู่ใน allowlist |
| queued/ready → blocked | orchestrator / PM(เสนอ) | ระบุ `blockedReason` (dependency, needsUserInput, quota, resource) |
| blocked → queued | orchestrator / user | เหตุผลถูกแก้ (event อ้างถึง) |
| ready → running | orchestrator | ได้ lease ของ repo (1 writer/repo), สร้าง attempt + worktree จาก `baseCommit`, budget คงเหลือ, quota band ≠ critical/reserve |
| running → verifying | **worker รายงาน** + orchestrator ยืนยัน | worker ส่ง `resultReported` + commit บน branch ของ attempt; orchestrator ตรวจว่ามี commit จริงและแก้เฉพาะ `allowedPaths` |
| running → failed | orchestrator | process exit ≠0, lease หมด, budget หมด หลัง retry ครบ |
| running → queued | orchestrator | lease หมด/crash และ `attempts < maxAttempts` (สร้าง attempt ใหม่) |
| verifying → awaitingReview | orchestrator | test command (จาก trusted base) ผ่าน, diff ไม่แตะ tests/CI/rules ที่ห้าม, scanner ไม่มี finding ใหม่ ≥ high |
| verifying → running/queued | orchestrator | verify fail → rework attempt ใหม่ (นับ attempt) |
| awaitingReview → completed | **user** หรือ reviewer อิสระตาม policy | review approved + base SHA ยังตรง (ถ้าไม่ตรง → กลับ verifying) |
| awaitingReview → queued | user/reviewer | revise → `taskRevision+1` พร้อม reason |
| * → cancelled | user | ส่ง cancel ให้ worker (SIGTERM ผ่าน adapter), เก็บ worktree ไว้เป็นหลักฐาน |

**worker ไม่มีทางทำให้ task เป็น `completed` ได้เอง** และคำตอบภาษาธรรมชาติ/terminal prompt ไม่ถือเป็น completion

## 5. Worker contract

### 5.1 ชนิด worker

| adapter | ใช้เมื่อ | delivery | completion signal | identity proof |
|---|---|---|---|---|
| `claudeHeadless` | unattended (orchestrator เป็นเจ้าของ) | `claude -p --output-format stream-json --session-id <uuid> --max-budget-usd N --allowedTools ... --add-dir <worktree>` fixed argv, prompt ทาง stdin | `result` event ใน stream + exit code | เราเป็น parent process เอง (pid + startTime) |
| `codexExec` | unattended (เมื่อมี `codex` CLI — **ตอนนี้ไม่มีบนเครื่อง**) | `codex exec --json` (ต้องตรวจ flags เมื่อติดตั้ง) | JSON event + exit code | parent process |
| `tmuxBound` | session ที่ผู้ใช้เปิดเองและ **bind อย่างชัดเจน** | paste-buffer (agcmd เดิม) หลังแก้ §5.3 | user turn ใหม่ใน transcript (ack) + PM อ่านผล แต่ **ต้องให้ผู้ใช้ยืนยัน** เข้าสู่ verifying | pane id `%N` + pid + startTime + `comm` ∈ allowlist |
| `observe` | session อื่นทั้งหมด | ไม่มี | — | — |

ข้อตัดสินใจ (D-2): **managed worker ใช้ headless เท่านั้น** เพราะ tmux path ไม่มี ack/readiness การแก้ tmux ยังจำเป็นสำหรับ `tmuxBound` แต่ไม่อยู่บน critical path ของ unattended

### 5.2 ข้อความระหว่าง orchestrator ↔ worker

Dispatch envelope (เขียนเป็นไฟล์ `<worktree>/.aiagent/task.json` + ส่งเป็น prompt):

```json
{
  "projectId": "aiagent", "taskId": "T-0007", "taskRevision": 2,
  "runId": "R-20261114-0003", "attemptId": "A-01HF...", "baseCommit": "90ecac5...",
  "workerId": "claudeHeadless@macMini", "sessionId": "uuid",
  "acceptanceCriteria": ["..."], "allowedPaths": ["internal/world/**"],
  "budget": {"maxUsd": 3, "maxMinutes": 45}, "deadlineAt": "...", "leaseExpiresAt": "...",
  "policyVersion": "p-2026-11-14", "reportSchema": "workerResult.v1"
}
```

Worker events (orchestrator สกัดจาก stream-json → typed event):
`attemptStarted`, `heartbeat`, `progress{summary}`, `artifactProduced{kind,path,sha256}`, `blocked{reason, question}`, `resultReported{commit, summary, claims[]}`, `attemptExited{code, signal}`

ทุก event มี `eventId`, `sequence`, `at`, `actor`, `attemptId` และ `idempotencyKey = attemptId + streamOffset` → duplicate ถูกตัดที่ UNIQUE constraint

### 5.3 แก้ tmux delivery (สำหรับ tmuxBound)

1. target เป็น pane id (`%N`) ที่ bind ไว้ + ตรวจ pid/startTime/comm ก่อน paste และหลัง paste ก่อน Enter
2. **allowlist** `comm ∈ {claude, codex, node(+argv0 ตรงกับ CLI)}`
3. ใช้ `=session:` exact match
4. per-session mutex ที่ orchestrator (ทุก sender: HTTP, outbox, policy, idle ideas ผ่านทางเดียวกัน)
5. ack = transcript มี user turn ที่ digest ตรง ภายใน N วินาที ไม่งั้น `deliveries.state=unknown`

## 6. Leases, retry, recovery

- **Repo lease**: 1 writer ต่อ repo; row `leases(repo, attemptId, expiresAt)`; heartbeat ทุก 30s จาก stream activity หรือ process alive (procinfo) ; หมดอายุ 5 นาทีไม่มี heartbeat → reaper
- **Retry**: `maxAttempts` default 2 ต่อ taskRevision, backoff 1m→5m; ไม่ retry อัตโนมัติถ้า exitReason ∈ {budgetExceeded, policyViolation, needsUserInput}
- **Delivery state machine**: `pending → sending → sent → acked`; crash ขณะ `sending` → ตอน startup ตั้งเป็น `unknown`; `unknown` → reconcile (ดู transcript/stream/process ของ attempt เดิม) **ห้ามส่งซ้ำทันที**
- **Startup recovery** (ลำดับ):
  1. เปิด DB, `PRAGMA integrity_check` (ล้มเหลว → read-only mode + แจ้ง UI)
  2. attempts ที่ `running`: ตรวจ process (pid+startTime ผ่าน procinfo) ถ้ายังอยู่ → re-adopt stream ไม่ได้ (pipe หาย) จึง **รอ exit แล้วอ่าน transcript ของ sessionId**; ถ้าไม่อยู่ → `attemptExited{reason: orphaned}` แล้วใช้ retry rule
  3. deliveries `sending` → `unknown`
  4. leases หมดอายุ → ปล่อย
- **Base SHA**: ก่อน `awaitingReview → completed` และก่อน merge ตรวจว่า target branch HEAD = `baseCommit` หรือ rebase แล้ว verify ใหม่
- **Cancellation**: SIGTERM → 10s → SIGKILL ที่ process group ของ worker, เก็บ worktree, event `cancelled`

## 7. Policy & security

- **PM ไม่มีสิทธิ์ execute**: planner คืน `proposedActions[]` ชนิดจำกัด (`createTask`, `updateTask`, `requestUserInput`, `proposeDispatch`, `proposeBlock`, `summarize`) orchestrator ตรวจ policy ก่อนทำ
- **Hard escalation rules** (ไม่พึ่ง self-reported confidence): riskLabels ∈ {auth, authz, crypto, migration, secretLeak, deletesData, ci, deploy} หรือ diff > N บรรทัด หรือ schema invalid หลัง retry 2 → advisor (Gemini/Claude cloud) + ผู้ใช้
- **Worktree ไม่ใช่ sandbox**: pilot ใช้ `--allowedTools`/`--disallowedTools` + `--add-dir` เฉพาะ worktree + ไม่ส่ง env ลับ (env allowlist เหมือน LaunchAgent) ภายหลังพิจารณา `sandbox-exec` profile หรือ container สำหรับ unattended night jobs
- **Test integrity**: test command + scanner rules อ่านจาก trusted base (`baseCommit` ของ default branch) ไม่ใช่จาก branch worker; diff ที่แตะ `*_test.go` ที่มีอยู่, `.github/**`, scanner config → ต้อง review โดยคน
- **Audit**: ทุก dispatch/cancel/merge/policy change เขียน `internal/audit` intent→result (รวม background sender ที่ตอนนี้ข้าม)
- **Remote** (D-3): web UI เข้าถึงจากภายนอกด้วย token login (ตัดสิน 2026-11-14); endpoint ใหม่ทุกตัวถือว่า internet-reachable ต้องผ่าน token gate, rate limit ต่อ client และ audit
- **Secrets**: worker env ไม่มี GitHub token จนกว่า Phase F; redaction เดิมใช้กับ event payload ก่อนเข้า DB/UI; audit `Err` ตัดเหลือ error code + digest

## 8. PM (planner)

- Skills: `intakeFeedback`, `planProject`, `prepareDispatch`, `trackProgress`, `triageFinding`, `summarizeReview` — แต่ละ skill = `{name, version, inputSchema, outputSchema, examples[], limits{maxInputTokens, maxOutputTokens, timeoutMs}}` เก็บใน `internal/planner/skills/*.json`
- Adapter order: `localPM` (ถ้า pilot ผ่าน) → `cloudPM` = `claude -p --json-schema <outputSchema> --max-budget-usd` (มีอยู่แล้วบนเครื่อง ยืนยัน flag ใน claude 2.1.318) → `geminiAdvisor` (ยังไม่ติดตั้ง)
- เพราะเครื่องตอนนี้ RAM ตึง **pilot แรกใช้ cloudPM** และ localPM เป็น experiment แยก (ดู `modelPilotPlan.md`)

## 9. Nightly security remediation (design only)

```
22:00–05:00 Asia/Bangkok window, เริ่ม 01:30 (ไม่ชนการใช้งานปกติ — ต้องยืนยันกับผู้ใช้)
scan(allowlist repos) → normalize(fingerprint=tool+ruleId+package+location hash) → dedupe กับ findings เดิม
→ triage(PM triageFinding; hard rules) → task(kind=security) → managed worker patch ใน worktree
→ verify(tests + rescan same tool/DB) → PR (ปิดไว้จนกว่าได้สิทธิ์ GitHub) → [merge: ปิด] → [deploy verify: ปิด]
```

- Scanner: `govulncheck` (Go, reachability จาก call graph = `reachabilityEvidence`), `trivy fs` (vuln/secret/misconfig), `gitleaks` (มีแล้วบนเครื่อง) — **ยังไม่ติดตั้ง govulncheck/trivy**
- scan ที่ tool ล่ม หรือ DB เก่ากว่า 48 ชม. → `scans.status=failed|staleDb` ไม่ใช่ clean
- finding states แยก `fixedInBranch → merged → deployed → verified`; ปิดได้เมื่อ rescan บน default branch ไม่พบ fingerprint
- secretLeak → task ชนิด `rotateSecret` ที่ต้องทำโดยคน (revoke/rotate) ไม่ใช่แค่ลบ string
- Resource gate: เริ่มได้เมื่อ disk ว่าง ≥ 15 GB, swap used < 50%, ไม่มี Xcode build ทำงาน

## 10. Feedback

feedback → PM `intakeFeedback` ร่าง task + acceptance criteria → **ผู้ใช้ยืนยัน** → task; rework = `taskRevision+1` + reason; project memory = ไฟล์ `docs/pmAgent/memory/<projectId>.md` ที่ผู้ใช้อนุมัติเท่านั้น; metric: reopened tasks, regressions, accepted patches, feedback→task traceability

## 11. Open decisions

| id | เรื่อง | ทางเลือก | ข้อเสนอ |
|---|---|---|---|
| D-1 | SQLite driver | modernc vs mattn | **ตัดสินแล้ว:** modernc (วัด binary size ใน B1) |
| D-2 | managed worker transport | headless vs tmux | **ตัดสินแล้ว:** headless |
| D-3 | การเข้าถึงจากภายนอก | Access / Tailscale / token | **ตัดสินแล้ว:** token login |
| D-4 | Codex CLI | ติดตั้ง / ข้าม | ข้ามจนกว่าจะติดตั้ง |
| D-5 | local PM | pilot ตอนนี้ / ภายหลัง | cloud PM ก่อน, local หลังผ่าน gate |

## 12. แก้ไขหลัง review (มีผลเหนือ §1–§11 เมื่อขัดกัน)

มาจาก independent design review ภายใน

1. **ขอบเขต v0.1 ลดลง:** Claude headless worker หนึ่งตัว, repo เดียว, ใน sandbox, ผู้ใช้อนุมัติทุก dispatch และทุก `completed` — ตัด `codexExec`, `deliveries` table, `findings`/`scans`, `feedback`, `task_revisions`, LLM reviewer ที่ตัดสินได้, nightly (เลื่อน)
2. **Sandbox เป็นเงื่อนไขก่อน unattended:** worker + verify รันใน `sandbox-exec` profile หรือ macOS user แยก: เขียนได้เฉพาะ worktree + temp; deny `<stateDir>`, `~/.cloudflared`, `~/.ssh`, keychain; network ตาม profile (verify = off)
3. **argv ของ worker:** `claude -p --output-format stream-json --session-id <uuid> --tools <ชุดที่อนุญาต> --permission-prompts none --strict-mcp-config --setting-sources "" --settings <ไฟล์ของ orchestrator ที่ validate แล้ว>` cwd = worktree; stdout → `<attemptDir>/stream.jsonl` (ไฟล์), `setsid`
4. **argv ของ cloudPM:** `claude -p --json-schema <schema> --tools "" --strict-mcp-config --setting-sources "" --no-session-persistence --permission-prompts none` cwd = temp dir ว่าง, prompt ทาง stdin
5. **Recovery:** attempt row `starting` ก่อน spawn → `running` หลังได้ pid+startTime; heartbeat = stream file โต; lease หมด → kill process group + ยืนยันตาย → ค่อย retry; `maxMinutes` = hard kill; idempotencyKey = `attemptId + byteOffset` ใน stream.jsonl (ไฟล์คงอยู่หลัง restart)
6. **State machine:** dependency fail/cancel → `blocked(dependencyFailed)`; timer ปลด `blocked(quota|resource)`; startup re-run verify ที่ค้าง; ปล่อย repo lease เมื่อ `awaitingReview`; base SHA เปลี่ยน → attempt ใหม่ (นับ `maxAttempts`); `awaitingReview → completed` actor = **user เท่านั้น**
7. **Verify:** test command + baseline test list จาก trusted base; `go test -json` ทุก baseline test ต้อง `pass`; flag `TestMain`/`init`/build tags/`go.mod`/`go.sum`/`vendor/`/`testdata/`/`.claude/**`/`.mcp.json`/`CLAUDE.md`/`.github/**` → review โดยคน
8. **Remote:** endpoint ใหม่ (`/api/tasks`) ถือว่า internet-reachable — เปิด write หลังงาน hardening ของ token gate (A9) เสร็จ
9. **Events:** append-only log; rows ใน `tasks`/`attempts` เป็น authoritative (ไม่มี projection rebuild ใน v0.1)
10. **Secrets:** secret finding เก็บ fingerprint + file:line เท่านั้น; audit error = code + digest; audit file 0600

## 13. โมเดลการส่งงานจริง: CLI แบบ subscription + PM เลือก agent (ผู้ใช้ชี้แจง 2026-11-14)

มีผลเหนือ D-2 และ §5.1/§12.3 เมื่อขัดกัน

- **ไม่มี API key**: claude, codex และ gemini ใช้ CLI ที่ login แบบ subscription เท่านั้น → ห้ามออกแบบที่ต้องใช้ `ANTHROPIC_API_KEY`/`--bare`/`--max-budget-usd` หรือ API ของ provider โดยตรง
- **PM กลางเลือก agent ต่องาน** แล้ว orchestrator **ส่งคำสั่งเข้า session ของ agent นั้น** (tmux pane ที่ CLI รันแบบ interactive อยู่) → การส่งผ่าน tmux คือเส้นทางหลัก ไม่ใช่ทางเสริม ดังนั้น Phase A (allowlist ตาม executable, pane id, per-session lock, ack, outbox ไม่ส่งซ้ำ) เป็นงานบน critical path
- **ฐานที่มีแล้ว**: `internal/world/route.go` (`pickWorkspace`) เลือก workspace ตาม provider และ quota (`auto` = ใช้ quota น้อยก่อน, ไม่ทราบไปท้าย) → PM เสนอ provider, orchestrator ใช้ `pickWorkspace` + เงื่อนไขเพิ่ม: CLI พร้อมและ login อยู่, ไม่มีงานอื่นถือ session นั้น (lease), quota band ≠ critical/reserve; **ไม่ทราบ = ไม่ส่ง** เว้นแต่ผู้ใช้สั่งเอง
- **Gemini**: ตอนนี้ provider `gemini` ถูกปฏิเสธโดยตั้งใจ (`route_test.go:81,110`) และเครื่องยังไม่มี `gemini` CLI → งานเพิ่ม: collector สำหรับ transcript ของ Gemini CLI, quota = "ไม่ทราบ" จนกว่าจะอ่านค่าจริงได้, เครื่องหมาย `◇` ใน UI
- **ผลงานจาก session แบบ interactive**: completion ไม่เชื่อข้อความตอบ — envelope สั่งให้ agent commit บน branch ของ attempt และเขียน `.aiagent/result.json` ใน worktree; orchestrator ยืนยันด้วย git object + test จาก trusted base เหมือน §12.7
- **PM เอง**: PM reasoning เรียกผ่าน CLI แบบ subscription (`claude -p` พร้อม flag ใน §12.4 — ใช้ OAuth ของ subscription ได้ ไม่ต้อง API key) หรือ session PM เฉพาะ; local model ยังเป็น pilot แยก
- **Sandbox**: session ที่ orchestrator เปิดเองรันใต้ `sandbox-exec` profile (§12.2); session ที่ผู้ใช้เปิดเองเป็น observe/bound เท่านั้น
