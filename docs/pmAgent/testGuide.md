# คู่มือทดสอบ Phase A–C (ทดสอบทีเดียวทั้งชุด)

ใช้กับ branch `feat/phase-b-taskstore` (PR #5) ซึ่งรวม Phase A (PR #4) ไว้แล้ว — ถ้า merge #4 และ #5 เข้า main แล้ว ใช้ main ได้เลย
คำสั่งทุกบล็อกไม่มี comment จึง paste ใน zsh ได้ตรงๆ

## 0. สิ่งที่ต้องรู้ก่อน

- **agent จะสร้าง branch ใน checkout ของ workspace ที่ได้รับงาน** (`git switch -c aiagent/<task>/<attempt>`) ถ้า workspace ชี้ไปที่ checkout หลักที่คุณใช้อยู่ branch ของมันจะเปลี่ยน → ทดสอบด้วย workspace แยก (ข้อ 2)
- ตอนนี้ Mac mini มีแค่ `claude` — งานที่ระบุ provider `codex` หรือ `gemini` จะ dispatch ไม่ได้ (ได้ 409 "no configured … workspace has a running agent") จนกว่าจะติดตั้งและ login CLI นั้น
- ระบบงาน**ไม่ส่งอะไรเอง**: ทุกการส่งงานต้องกด "อนุมัติเริ่มงาน" และทุกงานเสร็จต้องกด "อนุมัติ — เสร็จแล้ว"

## 1. build + ติดตั้ง + restart

```
cd ~/Projects/aiagent
git fetch origin
git switch feat/phase-b-taskstore
git pull --ff-only
go test -count=1 ./...
./packaging/install.sh
launchctl kickstart -k gui/$(id -u)/com.aiagent.service
sleep 2
tail -5 ~/Library/Logs/aiagent.log 2>/dev/null; launchctl list | grep com.aiagent.service
```

ถ้า log ของ service อยู่ที่อื่น ดูจาก `StandardErrorPath` ใน `~/Library/LaunchAgents/com.aiagent.service.plist`

## 2. workspace สำหรับ agent โดยเฉพาะ (แนะนำ)

checkout แยกด้วย git worktree + tmux session ที่รัน claude ในนั้น:

```
git -C ~/Projects/aiagent worktree add ~/Projects/aiagent-agent main
tmux new-session -d -s aiagent-agent -n agent -c ~/Projects/aiagent-agent
tmux send-keys -t aiagent-agent:agent claude Enter
```

แล้วเพิ่ม entry นี้ใน `~/.local/state/aiagent/workspaces.json` (ใน array `workspaces`) — `testCommand` เป็น argv ไม่ใช่ shell string:

```json
{
  "name": "aiagent-agent",
  "tmuxSession": "aiagent-agent",
  "dir": "/Users/hotkhwan/Projects/aiagent-agent",
  "provider": "claude",
  "testCommand": ["go", "test", "-count=1", "./..."]
}
```

ถ้า workspace `aiagent` เดิมยังตั้ง `provider: claude` อยู่ ระบบอาจเลือกตัวนั้นก่อน → ระหว่างทดสอบให้ลบ `"provider"` ออกจาก workspace `aiagent` เดิม (มันจะยังใช้ส่งข้อความตามชื่อได้ แต่ไม่ถูกเลือกโดย routing) แล้ว restart:

```
launchctl kickstart -k gui/$(id -u)/com.aiagent.service
```

## 3. ทดสอบบนหน้าเว็บ (`aiagent.joinanny.com` → แท็บ "งาน")

| ขั้น | ทำ | ผลที่ควรเห็น |
|---|---|---|
| 1 | กด **+ งานใหม่** → ชื่องาน "ทดสอบ: เพิ่มบรรทัดใน README", โปรเจกต์ `aiagent`, ให้ใครทำ **Claude**, เกณฑ์ผ่าน "README มีบรรทัด 'tested by PM'", แก้ได้เฉพาะ `README.md` | การ์ด T-0001 ในคอลัมน์ **รอคิว** |
| 2 | รอไม่เกิน 20 วินาที | ย้ายไป **พร้อมเริ่ม** |
| 3 | คลิกการ์ด → **อนุมัติเริ่มงาน** → ยืนยัน | ข้อความถูกพิมพ์เข้า session `aiagent-agent`; การ์ดไป **กำลังทำ**; attempt เป็น "agent ได้รับงานแล้ว" (ถ้า transcript ยืนยันได้) หรือ "ไม่ทราบว่า agent ได้รับหรือยัง" |
| 4 | ถ้าขึ้น "ไม่ทราบ…" ให้ดูใน tmux ว่า agent ได้งานจริง แล้วกด **ยืนยันว่า agent ได้รับงานแล้ว** | attempt เป็น "agent ได้รับงานแล้ว" |
| 5 | รอ agent ทำงาน — มันจะ commit บน branch `aiagent/T-0001/T-0001-A1` และเขียน `.aiagent/results/T-0001-A1.json` | ภายใน ~20 วินาทีหลังไฟล์ผลปรากฏ: **กำลังตรวจ** → ระบบรัน `go test` ใน worktree ชั่วคราว → **รอคุณตรวจ** |
| 6 | คลิกการ์ด → ดู **หลักฐาน** | ผลเทสต์ "passed: go test -count=1 ./...", commit, ไฟล์ที่แก้ = README.md |
| 7 | กด **ขอแก้** + เหตุผล | กลับ **รอคิว** รุ่นที่ 2 → พร้อมเริ่ม → อนุมัติอีกครั้งได้ |
| 8 | หรือกด **อนุมัติ — เสร็จแล้ว** | **เสร็จแล้ว** (ไม่มีการ merge/deploy ใดๆ — branch ยังอยู่ใน worktree ให้คุณ merge เอง) |

### กรณีที่ควรลองด้วย

| กรณี | วิธี | ผลที่ควรเห็น |
|---|---|---|
| งานที่รองานอื่น | สร้างงาน B โดยกรอก blockedBy ไม่ได้จากฟอร์ม — ใช้ API: `curl -s -H "X-Aiagent-Token: $TOKEN" -H 'Content-Type: application/json' -X POST https://aiagent.joinanny.com/aiagent/api/tasks -d '{"projectId":"aiagent","title":"B","acceptanceCriteria":["x"],"blockedBy":["T-0001"]}'` | B ค้างที่ **รอคิว** จน T-0001 เสร็จ; ถ้ายกเลิก T-0001 → B เป็น **ติดขัด** พร้อมชื่อ T-0001 |
| แก้ไฟล์นอกขอบเขต | ให้งานแก้ได้เฉพาะ `README.md` แต่สั่งใน tmux ให้ agent แก้ไฟล์อื่นด้วย | **กลับเข้าคิว** พร้อมเหตุผล "changed files outside the allowed paths: …" |
| แตะไฟล์ test | งานที่ต้องแก้ `*_test.go` | ผ่านได้ แต่หลักฐานขึ้น "แตะไฟล์ที่ต้องให้คนดู" |
| agent ติดขัด | บอก agent ให้เขียน `{"status":"blocked","summary":"คำถาม"}` | **ล้มเหลว** พร้อมคำถาม → กด **ส่งกลับเข้าคิว** ได้ |
| restart กลางทาง | `launchctl kickstart -k …` ระหว่างที่งานกำลังทำ | งานยังอยู่ครบ; attempt ที่ยังไม่ยืนยันกลายเป็น "ไม่ทราบ…" และ**ไม่ถูกส่งซ้ำ** |
| ส่งข้อความเข้า session (Phase A) | ส่งข้อความจากแชตปกติ | toast บอก "ยืนยันจาก transcript" หรือ "ยังไม่ยืนยันว่าถึง" |

## 4. ถ้ามีปัญหา

- แท็บ "งาน" ขึ้น "ระบบงานยังไม่เปิดบนเครื่องนี้" → binary ที่รันยังเป็นตัวเก่า หรือเปิด `tasks.jsonl` ไม่ได้ (ดู log ของ service: "task log unavailable")
- dispatch ได้ 409 "no configured claude workspace has a running agent" → tmux session ไม่มี window ชื่อ `agent` หรือ pane ไม่ได้รัน claude
- ข้อมูลงานทั้งหมดอยู่ที่ `~/.local/state/aiagent/tasks.jsonl` (หนึ่งบรรทัดต่อ event) — ลบไฟล์นี้ = เริ่มกระดานใหม่
