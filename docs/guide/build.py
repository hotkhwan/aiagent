#!/usr/bin/env python3
# docs/guide/build.py - the neutral guide as HTML: two manuals (from user.md and
# admin.md) and two slide decks (defined below). pdf.mjs prints them to
# ../../web/docs/*.pdf, which the web serves for download.
#   needs: pip install markdown-it-py   ·   node with playwright (for pdf.mjs)
import html, os, re
from markdown_it import MarkdownIt

HERE = os.path.dirname(os.path.abspath(__file__))
FONT = "".join(
    "@font-face{font-family:Mali;font-weight:%d;src:url('../../../web/fonts/mali-%d-%s.woff2') format('woff2');unicode-range:%s}" % (w, w, s, r)
    for w in (400, 600, 700)
    for s, r in (("thai", "U+0E01-0E5B,U+200C-200D,U+25CC"), ("latin", "U+0000-00FF,U+2000-206F,U+2191,U+2193,U+2212")))

MANUAL_CSS = FONT + """
@page{size:A4;margin:16mm 15mm}
*{box-sizing:border-box}body{font:11pt/1.6 Mali,sans-serif;color:#1A2238;margin:0}
h1{font-size:24pt;margin:0 0 4mm;color:#0B1026;border-bottom:3px solid #00C777;padding-bottom:3mm}
h2{font-size:15pt;margin:9mm 0 2mm;color:#0B1026;break-after:avoid}
h3{font-size:12pt;margin:5mm 0 1mm;break-after:avoid}
p,li{margin:1.5mm 0}ul,ol{padding-left:6mm}
table{width:100%;border-collapse:collapse;margin:3mm 0;font-size:10pt;break-inside:avoid}
th{background:#0B1026;color:#fff;text-align:left;padding:2mm 2.5mm}td{border-bottom:1px solid #D5DAE8;padding:2mm 2.5mm;vertical-align:top}
code{font-family:'DejaVu Sans Mono',monospace;font-size:9pt;background:#EEF1F8;border-radius:3px;padding:.3mm 1.2mm}
pre{background:#0B1026;color:#E6ECFF;border-radius:6px;padding:3mm 4mm;font-size:9pt;white-space:pre-wrap;break-inside:avoid}pre code{background:none;color:inherit;padding:0}
img{display:block;width:100%;border-radius:6px;border:1px solid #C8CFE0;margin:3mm 0;break-inside:avoid}img[src*=mobile]{width:55mm}img[src*=quota]{width:120mm}
"""

SLIDE_CSS = FONT + """
@page{size:1920px 1080px;margin:0}
*{box-sizing:border-box;margin:0}body{font-family:Mali,sans-serif;color:#E6ECFF;background:#0B1026}
section{width:1920px;height:1080px;padding:64px 84px;position:relative;overflow:hidden;break-after:page;display:flex;flex-direction:column;
  background:radial-gradient(1200px 700px at 85% -10%,#1B2A5E 0,#0B1026 60%)}
.eyebrow{font-size:26px;font-weight:600;color:#00DC82;letter-spacing:2px}
h1{font-size:112px;font-weight:700;line-height:1.1}h2{font-size:60px;font-weight:700;margin:6px 0 30px}
.sub{font-size:36px;color:#B9C6EA;margin-top:22px}
.pg{position:absolute;right:48px;bottom:30px;font-size:22px;color:#6F7FA8}
.cover{justify-content:flex-end;padding-bottom:110px}.cover .bg{position:absolute;inset:0;background-size:cover;background-position:center;opacity:.5}
.cover:after{content:'';position:absolute;inset:0;background:linear-gradient(0deg,#0B1026 12%,rgba(11,16,38,.15) 70%)}.cover>*{position:relative;z-index:1}
.shot{flex:1;min-height:0;display:flex;gap:40px;align-items:center}
.shot img{max-height:100%;max-width:100%;min-width:0;border-radius:18px;border:2px solid #2A3570;box-shadow:0 24px 60px rgba(0,0,0,.6)}
.shot .side{width:420px;flex:none;display:flex;flex-direction:column;gap:20px}
.chip{background:#141C33;border:2px solid #2A3570;border-radius:18px;padding:18px 22px;font-size:30px;line-height:1.3}
.chip b{display:block;color:#00DC82;font-size:24px;margin-bottom:2px}
.tiles{flex:1;display:grid;gap:32px;align-content:stretch;padding:10px 0 40px}.t2{grid-template-columns:1fr 1fr}.t3{grid-template-columns:repeat(3,1fr)}.t4{grid-template-columns:repeat(4,1fr)}
.tile{background:#141C33;border:3px solid #2A3570;border-radius:30px;padding:44px 38px;display:flex;flex-direction:column;gap:16px;align-items:flex-start;justify-content:center}
.tile .ic{font-size:130px;line-height:1.1}.tile b{font-size:52px;line-height:1.2}.tile span{font-size:34px;color:#B9C6EA;line-height:1.35}
.tile.ok{border-color:#00DC82}.tile.warn{border-color:#F5B84B}.tile.bad{border-color:#F07A8F}
.flow{flex:1;display:flex;align-items:center;justify-content:center;gap:18px}
.node{background:#141C33;border:3px solid #2A3570;border-radius:26px;padding:40px 34px;text-align:center;min-width:300px}
.node .ic{font-size:110px;line-height:1.1}.node b{display:block;font-size:42px;margin-top:8px}.node span{font-size:28px;color:#B9C6EA}
.node.hi{border-color:#00DC82;box-shadow:0 0 40px rgba(0,220,130,.25)}
.arr{font-size:60px;color:#00DC82;flex:none}
.stack{display:flex;flex-direction:column;gap:18px}.stack .node{padding:18px 26px;min-width:340px}.stack .node .ic{font-size:60px}.stack .node b{font-size:34px}.t6 .tile{padding:26px 30px}.t6 .tile .ic{font-size:84px}.t6 .tile b{font-size:42px}
.cmd{font-family:'DejaVu Sans Mono',monospace;font-size:38px;background:#060914;border:2px solid #2A3570;border-radius:20px;padding:34px 40px;color:#BDF7DE;line-height:1.7}
.cmd i{color:#6F7FA8;font-style:normal}
"""


def tile(ic, b, s="", cls=""):
    return '<div class="tile %s"><div class="ic">%s</div><b>%s</b><span>%s</span></div>' % (cls, ic, b, s)


def node(ic, b, s="", hi=False):
    return '<div class="node%s"><div class="ic">%s</div><b>%s</b><span>%s</span></div>' % (" hi" if hi else "", ic, b, s)


ARR = '<div class="arr">➜</div>'


def cover(title, sub):
    return '<section class="cover"><div class="bg" style="background-image:url(../img/village.jpg)"></div><p class="eyebrow">AI AGENT WORLD</p><h1>%s</h1><p class="sub">%s</p></section>' % (title, sub)


def shot(eyebrow, title, img, chips=()):
    side = "".join('<div class="chip"><b>%s</b>%s</div>' % c for c in chips)
    return '<section><p class="eyebrow">%s</p><h2>%s</h2><div class="shot"><img src="../img/%s.jpg">%s</div></section>' % (
        eyebrow, title, img, '<div class="side">%s</div>' % side if side else "")


def tiles(eyebrow, title, n, items):
    return '<section><p class="eyebrow">%s</p><h2>%s</h2><div class="tiles t%d%s">%s</div></section>' % (eyebrow, title, n // 10 if n > 9 else n, " t6" if n > 9 else "", "".join(tile(*i) for i in items))


def flow(eyebrow, title, body, sub=""):
    return '<section><p class="eyebrow">%s</p><h2>%s</h2><div class="flow">%s</div>%s</section>' % (eyebrow, title, body, '<p class="sub">%s</p>' % sub if sub else "")


USER = [
    cover("สั่งงาน AI agent<br>จากหน้าเว็บ", "สำหรับผู้ใช้ห้อง"),
    flow("ภาพรวม", "คุณ → เว็บ → ห้อง → agent",
         node("🧑‍💻", "คุณ", "เบราว์เซอร์ หรือมือถือ") + ARR + node("🌐", "เว็บ", "token ของห้อง") + ARR + node("🚪", "ห้องของคุณ", "พื้นที่ทำงานแยก", True) + ARR + node("🤖", "Agent", "อ่าน แก้ รัน ตามที่สั่ง")),
    shot("เริ่มต้น", "วาง token แล้วเข้าใช้งาน", "login"),
    shot("หน้าจอ", "รายการ agent และบทสนทนา", "list-user", (("ซ้าย", "agent ของคุณ และห้องอื่น"), ("ขวา", "บทสนทนา"), ("ล่าง", "พิมพ์สั่งงาน"))),
    shot("หน้าจอ", "หมู่บ้าน: agent อยู่ห้องไหน ทำอะไร", "room"),
    tiles("สถานะ", "ดูสีแล้วรู้ว่าต้องทำอะไร", 3, [
        ("🟢", "กำลังทำงาน", "รอ หรือพิมพ์ต่อได้", "ok"), ("🟡", "รอคุณตอบ", "ตอบในช่องพิมพ์", "warn"), ("⚪", "ว่าง", "ส่งงานใหม่ได้", "")]),
    tiles("สั่งงาน", "งานที่ดีมี 4 อย่าง", 4, [
        ("🎯", "ทำอะไร", "เรื่องเดียว ชัดเจน"), ("✅", "เสร็จเมื่อไร", "วัดได้ เช่น test ผ่าน"), ("⛔", "ห้ามแตะ", "ไฟล์หรือระบบที่ห้ามแก้"), ("📸", "หลักฐาน", "ผล test ภาพก่อนหลัง")]),
    shot("ตอบคำถาม", "agent ถาม ตอบจากเว็บได้เลย", "ask", (("หลายข้อ", "เห็นทุกข้อพร้อมกัน"), ("เลือกหลายข้อ", "ช่องติ๊ก"), ("พิมพ์เอง", "ช่องใต้ตัวเลือก"), ("ส่งคำตอบ", "ครั้งเดียวทุกข้อ"))),
    tiles("ส่งข้อความ", "ส่งครั้งเดียว เห็นสถานะชัด", 3, [
        ("⏳", "กำลังส่ง…", "กดซ้ำไม่ได้จนระบบตอบ", ""), ("🔁", "ข้อความนี้เพิ่งส่งไป", "ตั้งใจจริงค่อยกด ส่งซ้ำ", "warn"), ("⚠️", "ยังไม่ยืนยันว่าถึง agent", "เปิดบทสนทนาดูก่อนส่งซ้ำ", "bad")]),
    shot("พรีวิว", "ดูหน้าที่ agent สร้าง ทุกความกว้าง", "preview", (("390 · 768 · 1440", "มือถือ ถึงจอกว้าง"), ("จับภาพ", "390 และ 1440 px"), ("ส่งเข้ารีวิว", "ตรวจทีละข้อ"))),
    shot("ตรวจงาน", "ผ่าน หรือ ต้องแก้ไข ทีละข้อ", "review"),
    shot("Quota", "ป้ายเดียว กดดูรายละเอียด", "quota"),
    shot("สิทธิ์", "ห้องอื่นอ่านได้ ส่งไม่ได้", "mate", (("ของคุณ", "อ่าน ส่ง หยุด"), ("ห้องอื่น", "อ่านอย่างเดียว"), ("admin", "เห็นแค่สถานะ"))),
    tiles("ความปลอดภัย", "สามข้อที่ไม่มีข้อยกเว้น", 3, [
        ("🔑", "ไม่ขอดู secret", "token password .env", "bad"), ("📤", "ไม่ copy โค้ดออก", "ส่งงานตามช่องทางที่กำหนด", "bad"), ("💬", "ไม่วาง secret ในแชต", "ข้อความถูกเก็บไว้", "bad")]),
    shot("มือถือ", "ใช้ได้ครบบนมือถือ", "mobile"),
]

ADMIN = [
    cover("ดูแล AI Agent World", "สำหรับผู้ดูแลระบบ"),
    flow("ภาพรวม", "หนึ่งเครื่อง หลาย agent หลายห้อง",
         node("🌐", "aiagent", "เว็บ + API", True) + ARR +
         '<div class="stack">' + node("🪟", "tmux ของ site", "Codex · Antigravity") + node("🚪", "ห้อง × N", "OS user + Claude") + "</div>" + ARR +
         '<div class="stack">' + node("☁️", "Claude", "") + node("☁️", "Codex", "") + node("☁️", "Antigravity", "") + "</div>"),
    '<section><p class="eyebrow">ติดตั้ง</p><h2>คำสั่งเดียวบนเครื่องเปล่า</h2><div class="tiles"><div class="cmd">git clone https://github.com/hotkhwan/aiagent.git<br>cd aiagent<br>sudo ./packaging/setup.sh --site <i>ชื่อไซต์</i></div></div><p class="sub">ลงสิ่งที่ขาด · สร้างบริการ · พิมพ์ token admin หนึ่งครั้ง</p></section>',
    tiles("ครั้งแรก", "สามขั้นหลังติดตั้ง", 3, [
        ("1️⃣", "Token ของ Claude", "claude setup-token แล้ว aiagent-claude-token set"), ("2️⃣", "เปิดเว็บ", "วาง token admin"), ("3️⃣", "บัญชี", "login Claude, Codex, Antigravity")]),
    shot("บัญชี", "login ทั้งสามเจ้าจากเว็บ", "accounts"),
    shot("ห้อง", "สร้าง เข้า และลบห้อง", "accounts-rooms", (("สร้าง token", "ได้ OS user และห้อง"), ("เพิกถอน", "สำรองแล้วลบ"), ("Model", "ตั้งเกณฑ์รายสัปดาห์"))),
    shot("สั่งงาน", "admin สั่งและหยุดห้องได้จากรายการ", "list-admin"),
    shot("Vault", "token ของบริการภายนอก ไม่มีการอ่านกลับ", "vault", (("เพิ่ม · แก้ไข · ลบ", "จากหน้าบัญชี"), ("บันทึกไม่ผ่าน", "บอกเหตุผล ข้อความเดิมยังอยู่"), ("git", "aiagent git-credential"))),
    flow("กติกากลาง", "แก้ที่เดียว ทุก agent เห็นใน 1 นาที",
         '<div class="stack">' + node("📜", "กติกา", "room-base.md") + node("🧩", "Skill", "~/.claude/skills") + node("🧠", "Memory", "share: rooms") + "</div>" + ARR + node("⏱️", "ทุก 1 นาที", "ข้ามไฟล์ที่มี secret", True) + ARR +
         '<div class="stack">' + node("🚪", "ทุกห้อง", "CLAUDE.md + skills") + node("🪟", "Codex · Antigravity", "AGENTS.md") + "</div>"),
    tiles("ป้องกัน", "ห้าชั้นรอบ agent", 36, [
        ("👤", "OS user ต่อห้อง", "ห้องอื่นแตะไฟล์ไม่ได้"), ("📦", "กรอบ Codex / agy", "ไม่เห็นไฟล์ของ root"), ("🚫", "ไม่มีเครือข่ายใน shell", "sandbox ของ CLI"),
        ("🔒", "settings ของห้อง", "ห้าม push curl scp ssh"), ("🙈", "เว็บซ่อน token", "ปิดคำสั่ง ! และ &amp;"), ("🧱", "Firewall", "หน้าที่ของเครื่อง", "warn")]),
    flow("Restart", "เครื่องดับแล้วกลับมาเอง",
         node("🔌", "เครื่องเปิด", "") + ARR + node("🪟", "tmux + หน้าต่าง", "ต่อบทสนทนาล่าสุด", True) + ARR + node("🤖", "Claude เบื้องหลัง", "id เดิม") + ARR + node("🚪", "ทุกห้อง", "login ยังอยู่")),
    '<section><p class="eyebrow">tmux</p><h2>ดู agent จริงจาก terminal</h2><div class="tiles t2"><div class="cmd">tmux attach -t <i>ชื่อไซต์</i></div><div class="stack"><div class="chip"><b>เมนู</b>พิมพ์เลข แล้ว Enter</div><div class="chip"><b>ออกจากห้อง</b>Ctrl-b Ctrl-b d</div><div class="chip"><b>ออกจาก tmux</b>Ctrl-b d</div></div></div></section>',
    '<section><p class="eyebrow">ถอน</p><h2>ถอนออกได้สองระดับ</h2><div class="tiles"><div class="cmd">sudo ./packaging/uninstall.sh <i># เก็บ login และบทสนทนา</i><br>sudo ./packaging/uninstall.sh --purge <i># ลบทั้งหมด</i></div></div></section>',
]


def deck(name, title, slides):
    body = "".join(s.replace("</section>", '<span class="pg">%d</span></section>' % (i + 1), 1) for i, s in enumerate(slides))
    os.makedirs(HERE + "/out", exist_ok=True)
    open(HERE + "/out/%s.html" % name, "w").write('<!doctype html><meta charset="utf-8"><title>%s</title><style>%s</style>%s' % (html.escape(title), SLIDE_CSS, body))


def manual(name, src):
    md = open(HERE + "/" + src).read()
    title = re.search(r"^# (.+)$", md, re.M)[1]
    b = MarkdownIt("commonmark").enable(["table"]).render(md).replace('src="img/', 'src="../img/')
    open(HERE + "/out/%s.html" % name, "w").write('<!doctype html><meta charset="utf-8"><title>%s</title><style>%s</style>%s' % (html.escape(title), MANUAL_CSS, b))


deck("user-slides", "AI Agent World: สไลด์ผู้ใช้", USER)
deck("admin-slides", "AI Agent World: สไลด์ผู้ดูแล", ADMIN)
manual("user-manual", "user.md")
manual("admin-manual", "admin.md")
