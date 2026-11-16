# Design Brief — PM / Task Orchestrator UI (สำหรับส่ง ChatGPT ทำ mockup)

วันที่: 2026-11-14 · ใช้คู่กับ `architectureV01.md` §12 และ `docs/REDESIGN_SPEC.md`

## วิธีใช้

1. เปิดแชต ChatGPT ใหม่ **หนึ่งแชตต่อทั้งชุด** (ภาพจะได้สไตล์เดียวกัน)
2. แนบภาพอ้างอิง 2 ไฟล์ตั้งแต่ข้อความแรก:
   - `docs/audit/style-reference.png` (สไตล์ตัวละคร)
   - `web/assets/bg-village.jpg` (โลก/โทนสี — ภาพหมู่บ้านต้นฉบับที่ย่อแล้ว)
3. วาง **§1 Shared prompt** เป็นข้อความแรก แล้ววาง prompt ของแต่ละหน้า (§3 เป็นต้นไป) ทีละข้อความ ขอภาพทีละหน้า
4. แต่ละหน้าขอ 2 ขนาด: **Desktop 1440×900** และ **Mobile 390×844** (ส่ง prompt เดิมแล้วต่อท้ายด้วย "now the mobile version")
5. เก็บภาพที่ได้ไว้ที่ `docs/incoming/pm-ui/<pageKey>-desktop.png` / `-mobile.png` แล้วส่งกลับมาให้ตรวจ

ภาพเหล่านี้เป็น **mockup สำหรับตัดสินใจ layout** ไม่ใช่ asset ที่จะใช้จริง ข้อความในภาพอาจเพี้ยน (โดยเฉพาะภาษาไทย) ไม่เป็นไร ให้ดูโครงสร้างเป็นหลัก

## กฎที่ mockup ห้ามละเมิด (ทุกหน้า)

| กฎ | เหตุผล |
|---|---|
| **ห้ามมี progress bar เป็น %** | ระบบคำนวณ % ไม่ได้จริง (`REDESIGN_SPEC.md` §5.1) — ใช้ "ขั้นที่ 3 จาก 6", เวลาที่ใช้ไป, หรือ event ล่าสุดแทน |
| ต้องมีสถานะ **"ไม่ทราบ" (Unknown)** ที่ดูต่างจากศูนย์/ว่าง | กฎ "Unknown is a value" |
| ปุ่ม **"เสร็จแล้ว/อนุมัติ"** มีแต่ผู้ใช้กด, ไม่มี agent กดแทน | worker/PM ทำให้งาน completed เองไม่ได้ |
| **ทุกงานที่ PM เสนอ ต้องมีปุ่ม "ยืนยันสร้างงาน"** | ผู้ใช้อนุมัติทุก task และทุก dispatch ใน v0.1 |
| ความเห็นของ AI reviewer ต้องติดป้าย **"คำแนะนำ (advisory)"** | LLM review ไม่ใช่ผู้ตัดสิน |
| ภาษา UI หลัก **ไทย** มีปุ่มสลับ EN | ตาม `web/i18n.js` |
| ไม่มีปุ่มลบ/สั่ง sub-agent | sub-agent เป็น view-only โดยตั้งใจ |

## 1. Shared prompt (วางเป็นข้อความแรก)

```
You are designing UI mockups for "AI Agent World", a web app (desktop + mobile PWA) where one person
manages AI software-engineering agents (Claude Code and Codex CLI) running on a Mac mini, through a
central "PM" agent that turns requests into tasks.

Visual style — match the two attached references exactly:
- Night-time cyberpunk + sakura fantasy: deep indigo/violet background (#140f2e → #2a1f5c), neon rim
  light, glowing cyan (#3ee6ff), hot pink (#ff4fb8), violet (#9b6bff) accents, soft pink cherry blossoms.
- Chunky chibi characters with heavy dark outlines and neon rim light (see style reference).
- UI panels are frosted dark glass cards with a 1px neon edge glow, 16px rounded corners, generous padding.
- Typography: rounded sans-serif that supports Thai (e.g. "IBM Plex Sans Thai" / "Noto Sans Thai").
  UI language is THAI; render Thai labels exactly as given in quotes. Small "TH | EN" toggle top-right.
- A small glowing hologram cat mascot = the PM agent (from the village reference). Use it as the PM avatar.

Task status colors (use consistently on every screen, as pill badges with the Thai label):
  queued "รอคิว" = lilac #b9a7ff · ready "พร้อมเริ่ม" = cyan #3ee6ff · running "กำลังทำ" = electric blue #4d7cff
  verifying "กำลังตรวจ" = amber #ffc04d · awaitingReview "รอคุณตรวจ" = hot pink #ff4fb8
  completed "เสร็จแล้ว" = mint #46f0a8 · blocked "ติดขัด" = orange #ff8a3d · failed "ล้มเหลว" = red #ff4d5e
  cancelled "ยกเลิก" = grey #8a86a6 · unknown "ไม่ทราบ" = striped grey with a "?" icon

Hard rules for every mockup:
- NEVER draw percentage progress bars. Show "ขั้น 3/6", elapsed time like "12 นาที", or the latest real
  event text instead.
- Every value that can be unknown must have a visible "ไม่ทราบ" state, distinct from zero.
- Only the human user can press approve/complete. AI suggestions are labeled "คำแนะนำ (advisory)".
- Keep it readable first, decorative second: art lives in headers, empty states and the mascot,
  not behind dense data.

Reply "ready" and wait. I will send one screen at a time. For each screen produce one 1440x900
desktop mockup unless I ask for mobile (390x844).
```

## 2. Navigation รวม (ใช้ทุกหน้า)

- Desktop: แถบซ้ายแคบ (icon + label): **"เซสชัน"** (หน้าแรกเดิม) · **"PM"** · **"งาน"** · **"รีวิว"** (badge จำนวนรอตรวจ) · **"ฟีดแบ็ก"** · **"เครื่อง & โควตา"** · **"หมู่บ้าน"**
- Mobile: tab bar ล่าง 5 ช่อง: เซสชัน · PM · งาน · รีวิว · เพิ่มเติม
- Header: ชื่อ host `Mac mini`, จุดสถานะการเชื่อมต่อ, energy/quota chip เดิม, TH|EN

## 3. หน้า: PM แชต (pageKey `pm`)

**จุดประสงค์:** ผู้ใช้พิมพ์คำขอ (ไทย/อังกฤษ) PM ตอบเป็นการ์ดข้อเสนองานที่มีโครงสร้าง ไม่ใช่ข้อความยาว

**ข้อมูลจริง:** ข้อความผู้ใช้, ภาพแนบ, การ์ดงานที่ PM เสนอ (title, project, kind, acceptance criteria, riskLabels, blockedBy), คำถามกลับเมื่อข้อมูลไม่พอ, ป้าย "ส่งต่อที่ปรึกษา" เมื่อ escalate

**สถานะ:** PM กำลังคิด (ไม่มีเวลาคาดเดา), schema ไม่ผ่าน → "PM ตอบไม่สมบูรณ์ ลองใหม่ 1/2", escalate, cloud/local PM badge

```
Screen: "PM" chat (desktop). Left: the global nav. Center: a chat thread with the hologram-cat PM avatar.
The user wrote in Thai: "อยากให้ส่งข้อความเข้า session ได้ชัวร์ว่าถึงจริง และกันส่งซ้ำ".
The PM replies with a short Thai sentence and TWO structured proposal cards (not paragraphs):
 Card 1 "แก้ outbox ไม่ให้ส่งซ้ำหลัง crash" · project "aiagent" · kind chip "bug" · risk chips none ·
   "เกณฑ์ผ่าน" checklist with 3 items (unchecked boxes) · buttons "ยืนยันสร้างงาน" (primary) and "แก้ไข".
 Card 2 "ยืนยันการส่งจาก transcript (ack)" · chip "feature" · "ขึ้นกับ: งานที่ 1" link · risk chip "delivery"
   · same buttons.
Below them a PM question bubble: "ต้องการให้ข้อความที่ส่งไม่สำเร็จแสดงที่ไหน?" with 2 quick-reply chips.
Small footer label on the PM bubble: "PM: cloud · skill planProject v1".
Right rail: "บริบทโปรเจกต์" panel (repo aiagent, branch main, allowlist paths) and a small "ประวัติคำขอ" list.
Bottom: composer with attach-image button and "ส่งถึง PM".
```

## 4. หน้า: กระดานงาน (pageKey `tasks`)

**จุดประสงค์:** เห็นทุก task ตามสถานะ

**ข้อมูลจริงบนการ์ด:** `taskId` (T-0007), title, project, worker badge (Claude/Codex), attempt `1/2`, เวลาที่ใช้ไป, event ล่าสุด, risk chips, blockedBy, ไอคอน "รอคุณ" เมื่อผู้ใช้ต้องทำอะไร

**สถานะ:** คอลัมน์ว่าง (มีภาพ mascot เล็ก), worker ไม่พร้อม (เช่น "ไม่พบ codex CLI"), quota critical → แถบเตือน "หยุดส่งงานชั่วคราว: โควตาต่ำ"

```
Screen: "งาน" task board (desktop). Header row: title "กระดานงาน", project filter "aiagent", search,
and a button "+ งานใหม่ผ่าน PM". A thin warning ribbon under the header (amber):
"Codex: ไม่พบ CLI บนเครื่อง — ส่งงานได้เฉพาะ Claude".
Kanban with 6 columns, each header shows the Thai status pill + count:
"รอคิว" · "พร้อมเริ่ม" · "กำลังทำ" · "กำลังตรวจ" · "รอคุณตรวจ" · "เสร็จแล้ว".
A collapsible bottom lane "ติดขัด / ล้มเหลว / ยกเลิก" with 2 cards.
Task cards (glass, compact): "T-0007" mono id, title in Thai, small Claude/Codex logo-free badge
("Claude"), "ครั้งที่ 1/2", "⏱ 12 นาที", latest event in grey: "รัน go test… 2 ไฟล์แก้แล้ว",
risk chips like "auth" in orange. Cards in "รอคุณตรวจ" have a pulsing pink edge and a "ตรวจเลย" button.
One "กำลังทำ" card shows a tiny chibi dev character typing. No percentage bars anywhere.
"เสร็จแล้ว" column is visually quieter. Right side: none (board uses full width).
```

## 5. หน้า: รายละเอียดงาน (pageKey `taskDetail`)

**จุดประสงค์:** ดูว่างานหนึ่งกำลังเป็นอย่างไร มีหลักฐานอะไร และสั่งยกเลิก/ลองใหม่

**ข้อมูลจริง:** status, taskRevision, acceptance criteria (ติ๊กได้เฉพาะเมื่อมีหลักฐานจาก verify), timeline ของ events (มีเวลา + actor), attempts (attemptId, baseCommit สั้น, branch, worker, เวลา, exitReason), artifacts (patch, test log), ขอบเขต: allowedPaths, sandbox profile, เวลาจำกัด (wall-clock), lease หมดอายุเมื่อ

```
Screen: task detail "T-0007 แก้ outbox ไม่ให้ส่งซ้ำหลัง crash" (desktop).
Top: breadcrumb "งาน › T-0007", status pill "กำลังตรวจ", "รุ่นที่ 2" revision chip, buttons
"ยกเลิกงาน" (ghost red) and "ดู diff".
A horizontal stepper using the 6 states with the current one glowing amber — labeled "ขั้น 4/6", no %.
Left column (60%): "เกณฑ์ผ่าน" checklist — 2 items checked with a small "หลักฐาน: test log" link,
1 item unchecked "รอผลตรวจ". Below: "ไทม์ไลน์" vertical event list with time, actor icon
(user / PM cat / worker / ระบบ) and short Thai text, e.g. "08:41 ระบบ · เริ่ม attempt A-02 จาก 90ecac5",
"08:52 worker · รายงานผล commit 3f2a1c9", "08:53 ระบบ · เริ่มรันเทสต์ (base ที่เชื่อถือได้)".
Right column (40%): "ความพยายาม" list (A-01 ล้มเหลว: หมดเวลา · A-02 กำลังตรวจ), "ขอบเขต" panel
(allowedPaths list, "sandbox: เขียนได้เฉพาะ worktree", "จำกัดเวลา 45 นาที · ใช้ไป 12 นาที"),
"ไฟล์ผลลัพธ์" panel (patch.diff, test-log.json, with sizes). Small chibi tester character
with a clipboard in the stepper's "กำลังตรวจ" node.
```

## 6. หน้า: ตรวจและอนุมัติ (pageKey `review`)

**จุดประสงค์:** ผู้ใช้ตัดสินใจว่างานผ่านหรือต้องแก้ — หน้าที่สำคัญที่สุด

**ข้อมูลจริง:** diff summary (ไฟล์, +/−), ผล test baseline (ทุกตัวต้อง pass, แสดงจำนวน pass/skip/fail), ธงเตือน protected paths (แก้ test เดิม, `.github/**`, `.claude/**`, go.mod), สถานะ base SHA (ตรง/เปลี่ยน), scanner (ถ้ามี) หรือ "ไม่ได้สแกน", ความเห็น AI (advisory)

```
Screen: "ตรวจงาน T-0007" review page (desktop). Top banner: status pill "รอคุณตรวจ" and a clear
human-decision area on the right with two big buttons: "อนุมัติ — เสร็จแล้ว" (mint) and
"ขอแก้" (pink outline) which opens a reason field "เหตุผลที่ขอแก้".
Left: "สรุปการเปลี่ยนแปลง" — file list with +/− line counts (internal/world/outbox.go +48 −12,
internal/world/world_test.go +60 −0), then a code diff viewer (dark, neon line numbers).
Right column cards:
 "ผลเทสต์ (base ที่เชื่อถือได้)": "43/43 ผ่าน · ข้าม 0 · ล้มเหลว 0" in mint;
 "ตรวจความปลอดภัยของ diff": rows with green checks — "ไม่แตะเทสต์เดิม", "ไม่แตะ CI", "ไม่แตะ .claude/",
   and one amber row "เพิ่มไฟล์เทสต์ใหม่ 1 ไฟล์ — โปรดดู";
 "Base commit": "ตรงกับ main (90ecac5)" with a green dot;
 "สแกนช่องโหว่": grey "ไม่ได้สแกน (ยังไม่ติดตั้ง)" — must look different from a pass;
 "คำแนะนำ (advisory) จาก AI reviewer": 2 short bullet notes, small disclaimer
   "ไม่ใช่ผู้ตัดสิน".
Decorative: a small chibi in the "รอคุณตอบ" approval-room style peeking from the header corner.
```

## 7. หน้า (modal): อนุมัติเริ่มงาน (pageKey `dispatchApprove`)

**จุดประสงค์:** ก่อน worker เริ่ม ผู้ใช้เห็นว่าจะรันอะไร ที่ไหน ด้วยสิทธิ์อะไร

```
Screen: a centered modal over the dimmed task board, title "อนุมัติเริ่มงาน T-0008".
Rows with icons: "Worker: Claude (headless)", "Repo: aiagent", "Branch ใหม่: aiagent/T-0008/A-01",
"เริ่มจาก commit: 90ecac5", "แก้ได้เฉพาะ: internal/world/**", "Sandbox: เขียนได้เฉพาะ worktree ·
ห้ามอ่าน ~/.ssh ~/.cloudflared", "จำกัดเวลา: 45 นาที", "โควตา Claude: ปกติ" (green band chip).
A collapsible "คำสั่งที่จะส่งให้ worker" preview (monospace, 4 lines).
Buttons: "อนุมัติและเริ่ม" (primary cyan) / "ยังไม่เริ่ม". Small note: "worker ทำให้งานเสร็จเองไม่ได้ —
ต้องผ่านการตรวจและคุณอนุมัติ".
```

## 8. หน้า: ฟีดแบ็ก (pageKey `feedback`)

```
Screen: "ส่งฟีดแบ็ก" (desktop) — two-pane. Left: form with segmented control
"บั๊ก · ฟีเจอร์ · UX · ถดถอย", fields "เกี่ยวกับงาน" (dropdown T-0007), "สิ่งที่คาดหวัง", "สิ่งที่เกิดขึ้นจริง",
image drop zone with one screenshot thumbnail, button "ส่งให้ PM ร่างงาน".
Right: PM draft preview card (hologram cat) — "ร่างงานจากฟีดแบ็ก": title, kind chip "ถดถอย",
"เกณฑ์ผ่าน" 2 items, priority "สูง", with buttons "ยืนยันสร้างงาน" / "แก้ไขร่าง".
Below the form: "ฟีดแบ็กก่อนหน้า" list with status chips (เปิด / กลายเป็นงาน T-0005 / ปิดแล้ว).
```

## 9. หน้า: เครื่อง & โควตา (pageKey `health`)

**ข้อมูลจริง:** disk ว่าง, memory pressure, swap ใช้ไป, CLI ที่พบ/ไม่พบ + version, slot ของ worker (1 ต่อ repo), quota band ของ Claude/Codex (ปกติ/ประหยัด/วิกฤต/สำรอง/ไม่ทราบ), PM model (cloud/local, สถานะ)

```
Screen: "เครื่อง & โควตา" dashboard (desktop), host card "Mac mini · M4 · 16 GB · macOS 26.6".
Stat tiles (no percentage bars; use plain numbers and a status word):
 "ดิสก์ว่าง 32 GiB" with word "พอใช้" (green), "แรงกดดันหน่วยความจำ: ปกติ", "Swap ใช้ไป 1.2 GB",
 "Worker ที่ทำงาน 1 / 1 ต่อ repo".
"CLI บนเครื่อง" table: claude 2.1.318 ✓ · codex "ไม่พบ" (orange) · gemini "ไม่พบ" · tmux 3.8 ✓.
"โควตา" cards for Claude and Codex, each a band chip: Claude "ปกติ" (5 ชม. / 7 วัน windows shown as
"รีเซ็ตใน 2 ชม."), Codex "ไม่ทราบ" (striped grey, "ยังไม่มีข้อมูล").
"PM" card: "ใช้ cloud PM · local PM: ยังไม่ติดตั้ง".
"ประตูความปลอดภัย" small card: "เข้าจากภายนอกด้วย token · ล็อกตาม IP จริง (Cloudflare)".
Mascot cat sleeping on top of the host card as decoration.
```

## 10. หน้า: หมู่บ้าน + งาน (pageKey `villageTasks`)

**จุดประสงค์:** เชื่อม task กับห้องเดิม 8 ห้อง — ตัวละครอยู่ห้องตามสถานะงาน

| สถานะงาน | ห้อง (key เดิม) |
|---|---|
| queued/ready (PM วางแผน) | `plan` ห้องวางแผน |
| running | `code` ห้องพัฒนา |
| verifying | `test` ห้องทดสอบ |
| awaitingReview | `approval` รอคุณตอบ |
| blocked | `ops` ห้องระบบ |
| completed / idle | `lounge` ห้องพัก |

```
Screen: the existing isometric interior with 8 neon rooms (plan, library, code, test, ops, deploy,
approval, lounge) as in the references. Chibi agents stand in rooms by task state: one dev typing in
"ห้องพัฒนา" with a floating chip "T-0008 · กำลังทำ · 12 นาที", one tester in "ห้องทดสอบ" with
"T-0007 · กำลังตรวจ", a pink glowing exclamation over "รอคุณตอบ" with chip "1 งานรอคุณตรวจ".
The hologram cat PM floats in "ห้องวางแผน" holding 2 small task cards.
A slim bottom drawer "งานในหมู่บ้าน" lists the same tasks. No percentage bars.
```

## 11. หน้า: เซสชัน (หน้าแรกเดิม) — ปรับเล็กน้อย (pageKey `home`)

```
Screen: the existing home "เซสชัน" list, updated: each session card gains a small chip when it is
bound to a task ("ผูกกับ T-0008") or "ดูอย่างเดียว" for sessions the user opened manually.
Header gets a compact "งานรอคุณ 1" pink pill linking to review. Keep everything else as the current
home list. Mobile layout preferred for this one (390x844).
```

## 12. (ภายหลัง — Phase F) หน้า: ช่องโหว่ (pageKey `findings`)

ยังไม่ต้องทำตอนนี้ ถ้าอยากเห็นภาพรวม:

```
Screen: "ช่องโหว่" list. Columns: severity chip, tool ("govulncheck"/"trivy"), package or file,
"สถานะ" as a 4-step trail "แก้ใน branch → merge แล้ว → deploy แล้ว → ยืนยันแล้ว".
A scan header "สแกนล่าสุด 01:30 · ฐานข้อมูล 6 ชม.ก่อน"; a failed scan shows red "สแกนล้มเหลว — ไม่นับว่าปลอดภัย".
Secret-type findings show only "พบความลับ · fingerprint a1b2… · ต้องหมุนคีย์" — never the value.
```

## ลำดับที่แนะนำให้สร้าง

1. `tasks` → 2. `review` → 3. `pm` → 4. `taskDetail` → 5. `dispatchApprove` → 6. `health` → 7. `villageTasks` → 8. `feedback` → 9. `home` (mobile)

สามหน้าแรกพอให้ตัดสินใจ layout หลักก่อนเริ่ม Phase A/B แล้ว
