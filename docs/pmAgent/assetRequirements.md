# Asset Requirements — PM / Task UI (v0.1)

วันที่: 2026-11-14 · อ้างอิง mockup ที่ผู้ใช้อนุมัติ:
[`village-pm-desktop.jpg`](../incoming/pm-ui/village-pm-desktop.jpg) ·
[`pm-chat-desktop.jpg`](../incoming/pm-ui/pm-chat-desktop.jpg) ·
[`tasks-review-desktop.jpg`](../incoming/pm-ui/tasks-review-desktop.jpg)

กฎทุกไฟล์ตาม [`docs/ASSET_SPEC.md`](../ASSET_SPEC.md) §Hard rules:
ชื่อไฟล์ `^[A-Za-z0-9-]{1,40}\.(png|jpg)$`, PNG มี alpha จริง (ยกเว้น `bg-*` เป็น JPG), ขนาด canvas ตรงตามตาราง,
**ห้ามโลโก้/เครื่องหมายการค้าจริง** (Anthropic, OpenAI, Google), ห้ามหน้าคนจริง/ตัวละครที่จำได้,
บันทึก provenance (tool, account, date, terms) ลง `docs/ASSETS.md` ทุก batch

**งบขนาด:** `web/assets` ตอนนี้ 12 MB (เกินเป้า 4.6 MB ใน ASSET_SPEC แล้ว) — ของใหม่ทั้งชุดต้อง **≤ 400 KB รวม**

## 1. ต้องทำ (art) — เรียงตามความสำคัญ

### 1.1 PM กลาง — แมวโฮโลแกรม (mascot)

ตัวเดียวกับรูปปั้นแมวกลางลานใน `web/assets/bg-village.jpg`: ตัวกลมโปร่งแสงสีฟ้า-cyan, หูแมว, หูฟัง, ตาโตเรืองแสง, เส้นขอบเข้ม + neon rim light แบบเดียวกับตัวละครอื่น

| ไฟล์ | ขนาด | ใช้ที่ | หมายเหตุ |
|---|---|---|---|
| `pm-avatar.png` | 256x256 | header ของแผง PM / หน้า PM (แสดงที่ 96px) | หน้าตรง, พื้นโปร่งใส |
| `pm-avatar-sm.png` | 64x64 | bubble ในแชต, รายการกิจกรรม, nav | อ่านออกที่ 32px — ลดรายละเอียด |
| `ch-pm-idle.png` | 512x128 (4 เฟรม) | ลอยในห้องวางแผนของหมู่บ้าน | ลอยขึ้นลงเบาๆ, ตามกฎ sprite sheet ใน ASSET_SPEC |
| `ch-pm-think.png` | 512x128 (4 เฟรม) | PM กำลังวางแผน/รอผล cloud | มีจุด … หรือวงแหวนหมุน |
| `ch-pm-alert.png` | 512x128 (4 เฟรม) | มีงานรอคุณตรวจ / escalate | กระพริบชมพู (#ff4fb8) |
| `ch-pm-sleep.png` | 512x128 (4 เฟรม) | ไม่มีงาน (empty state) | ใช้ร่วมกับ `fx-zz.png` ได้ |

### 1.2 หัวตัวละคร (avatar) สำหรับแถวงาน

mockup หน้า "งาน" ใช้หน้าตัวละคร chibi ต่อแถว — ใช้ **role เดิม 6 แบบ** (ไม่ทำตัวใหม่ต่อ provider) ครอปหัว+ไหล่จากสไตล์ sprite ปัจจุบัน

| ไฟล์ | ขนาด |
|---|---|
| `av-dev.png`, `av-designer.png`, `av-tester.png`, `av-research.png`, `av-infra.png`, `av-deploy.png` | 96x96 ทุกไฟล์ (แสดงที่ 40–48px) |

การเลือก avatar: ตาม **ชนิดงาน** (bug/feature → dev, ux → designer, verify → tester, research → research, ops → infra, deploy → deploy) ส่วน provider (Claude/Codex/Gemini) แสดงเป็น **ข้อความ** ไม่ใช่รูป

### 1.3 Banner หน้า "งาน"

| ไฟล์ | ขนาด | หมายเหตุ |
|---|---|---|
| `bg-tasks-banner.jpg` | 1200x240 | ทะเลสาบกลางคืน + สะพาน + ซากุระ + โคมไฟ ตาม mockup; ด้านซ้าย 45% ต้องมืดเรียบ (มีหัวข้อ/ช่องค้นหาวางทับ); JPG ≤ 90 KB |

### 1.4 Empty states

| ไฟล์ | ขนาด | ใช้ที่ |
|---|---|---|
| `empty-tasks.png` | 320x220 | กระดานงานว่าง: แมว PM นั่งข้างกระดานเปล่า |
| `empty-review.png` | 320x220 | ไม่มีงานรอตรวจ: แมว PM ถือป้ายติ๊กถูก |
| `empty-pm.png` | 320x220 | แชต PM ยังไม่มีข้อความ: แมว PM โบกมือ |

## 2. ไม่ต้องวาด — เป็นโค้ด (SVG/CSS)

ตาม `REDESIGN_SPEC.md` §2/§5: ไอคอนที่ต้องเปลี่ยนสีตามธีมเป็น SVG path ไม่ใช่ PNG

| สิ่งที่เห็นใน mockup | ทำอย่างไร |
|---|---|
| ไอคอน nav (หมู่บ้าน, รายการ, PM, งาน, รีวิว), ไอคอนในแผงตรวจ (ผลเทสต์, base commit, diff, scanner, advisory) | SVG path เส้นเดียว ใน `iconSvg()` |
| pill สถานะงาน (รอคิว / พร้อมเริ่ม / กำลังทำ / กำลังตรวจ / รอคุณตรวจ / เสร็จแล้ว / ติดขัด / ล้มเหลว / ยกเลิก / ไม่ทราบ) | CSS ตามสีใน `designBrief.md` §1 |
| chip บนแผนที่หมู่บ้าน ("วางแผน · 2 งาน", "ทดสอบ · T-0007 กำลังตรวจ") | DOM overlay + ตำแหน่ง hotspot ต่อห้องบนภาพหมู่บ้าน |
| แถบสถิติล่าง (กำลังทำงาน / รอคุณตอบ / ว่าง / งานในวันนี้ / รอคิว / ไม่ทราบ) | CSS |
| เครื่องหมาย provider | ข้อความกลาง: Claude `✻`, Codex `>_` (ใช้อยู่แล้ว), **Gemini `◇`** (ใหม่) — ห้ามใช้โลโก้จริง |
| โลโก้ header | ใช้ `icon-192.png` เดิม ย่อด้วย CSS |
| เครื่องหมาย ! เหนือห้องรอคุณตอบ | `fx-bang.png` เดิม |

## 3. ข้อสังเกตจากการตรวจ mockup

ผ่านกฎใน `designBrief.md`: ไม่มี progress bar เป็น %, มีสถานะ "ไม่ทราบ" (Codex quota), ความเห็น AI ติดป้าย "คำแนะนำ (Advisory) · ไม่ใช่ผู้ตัดสิน", ปุ่มอนุมัติ/ขอแก้เป็นของผู้ใช้, เกณฑ์ผ่านมีป้าย "รอหลักฐาน/ตรวจแล้ว", ผลเทสต์ติดป้าย "ข้อมูลตัวอย่าง"

ต้องปรับตอน implement:

1. ป้าย **"Local PM"** ต้องเป็นค่าจริง — v0.1 ใช้ PM ผ่าน CLI แบบ subscription จึงแสดง `PM: Claude CLI` (หรือ provider ที่ใช้อยู่) จนกว่า local model จะผ่าน pilot
2. แถว **"Worker: Codex"** ต้องแสดง "ไม่พร้อม" เมื่อเครื่องไม่มี CLI/ไม่ได้ login (ตอนนี้ Mac mini ไม่มี `codex` และ `gemini`)
3. chip ใน header ต้องมี **Gemini** ด้วย (สามตัว: Claude / Codex / Gemini) — แต่ละตัวแสดง band จริงหรือ "ไม่ทราบ"
4. tab "รายการ" = หน้า session list เดิม (หน้าแรก) — คง route เดิมไว้

## 4. ลำดับการทำ

1. `pm-avatar.png`, `pm-avatar-sm.png` (ใช้ได้ทันทีกับแผง PM)
2. `av-*.png` 6 ไฟล์
3. `empty-*.png` 3 ไฟล์
4. `bg-tasks-banner.jpg`
5. `ch-pm-*.png` 4 sheets (ต้องรอหน้าหมู่บ้านใหม่)

## 5. Prompt สำหรับ ChatGPT (แนบ `docs/audit/style-reference.png` + `web/assets/bg-village.jpg`)

```
Create a single character asset on a fully transparent background, no text, no logos.
Character: the "PM cat" — a small round translucent hologram robot-cat, cyan/teal glow (#3ee6ff),
cat ears, small headphones, big glowing eyes, simple smiling mouth, chunky chibi proportions,
heavy dark outline and neon rim light exactly like the attached style reference. It is the same
character as the glowing cat statue in the middle of the attached village painting.
Front view, centered, fills ~85% of a square canvas, no shadow on the ground, no background.
```

ต่อด้วยคำขอแยก: "same character, 4-frame horizontal sprite strip, 128x128 per frame, gently floating up and down" สำหรับ `ch-pm-idle` และเปลี่ยนท่าตามตาราง 1.1
