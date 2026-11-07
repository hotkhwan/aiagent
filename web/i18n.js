// Thai and English for the AI Agent World UI.
//
// The Thai string is the key. That is deliberate: it keeps the source
// readable — `tr('ห้องพัฒนา')` says what it renders, where `tr('room.code')`
// would not — and a missing entry degrades to Thai, which is a usable screen,
// rather than to a raw key, which is not.
//
// Two mechanisms, because there are two kinds of string:
//
//   tr(s)              for text app.js builds at runtime
//   applyStaticLang() for the markup in index.html, which never changes
//
// The second walks the document once at load, remembers every Thai text node
// and every Thai title/aria-label/placeholder, and swaps them on demand. That
// is why index.html needs no data-i18n attributes: the Thai already in it is
// the key. Anything the user owns — a session name, a workspace, a message —
// is built by app.js and is never passed through tr(), so it is never
// translated.

var LANG_KEY = 'aiagentLang'

var EN = {
  // agent and session state
  'กำลังเขียนโค้ด': 'Coding',
  'กำลังทดสอบ': 'Testing',
  'กำลัง Deploy': 'Deploying',
  'กำลังอ่าน/วิเคราะห์': 'Reading / analysing',
  'กำลังรันคำสั่ง': 'Running a command',
  'กำลังวางแผน': 'Planning',
  'ว่าง รอคำสั่ง': 'Idle, awaiting orders',
  'กำลังทำงาน': 'Working',
  'ทำงานอยู่': 'Running',
  'จบ session แล้ว': 'Session ended',
  'รอคุณตอบ': 'Waiting for you',
  'เสร็จแล้ว': 'Done',
  'กำลังทำ': 'In progress',
  'รอคุณตัดสินใจ': 'Awaiting your decision',
  'เสร็จ รอตรวจ': 'Done, awaiting review',
  'พัก (บันทึกไว้)': 'Paused (saved)',
  'ยกเลิกแล้ว': 'Cancelled',

  // energy and quota
  'ปกติ': 'Normal',
  'ประหยัด': 'Saving',
  'วิกฤต': 'Critical',
  'สำรองไว้': 'Reserved',
  'ไม่ทราบ': 'Unknown',
  'ยืนยันจาก process (pid + เวลาเริ่ม)': 'Confirmed from the process (pid + start time)',
  'ยืนยันจาก process ที่เปิดไฟล์ session': 'Confirmed from the process holding the session file',
  'มี process codex ในโฟลเดอร์เดียวกัน': 'A codex process in the same folder',
  'ไม่มี process ทำงานอยู่': 'No process running',

  // relative time. Thai spells the unit out; English is tighter as a suffix.
  ' วินาทีที่แล้ว': 's ago',
  ' นาทีที่แล้ว': 'm ago',
  ' ชั่วโมงที่แล้ว': 'h ago',
  ' วันที่แล้ว': 'd ago',

  // connection
  'สด · ': 'Live · ',
  'ขาดการเชื่อมต่อ · ข้อมูลล่าสุด ': 'Disconnected · last data ',
  'ยังเชื่อมต่อไม่ได้': 'Still cannot connect',
  'ขาดการเชื่อมต่อกับบริการ — ภาพด้านล่างเป็นข้อมูลเก่า ไม่ใช่สถานะปัจจุบัน':
    'Disconnected from the service — what is shown below is old data, not the current state',
  'AI กำลังประหยัดพลังงาน — ': 'AI is saving energy — ',
  ' ใช้ไป ': ' used ',
  ' · รีเซ็ต ': ' · resets ',

  // the village and the map
  'หมู่บ้าน Agent': 'Agent Village',
  'จอมอนิเตอร์ห้อง · ข้อความล่าสุดของ agent ในห้องนี้ · คลิก agent เพื่อเปิดแชต · Esc/← กลับ':
    'Room monitor · the latest message from every agent here · click an agent to open the chat · Esc/← to go back',
  ' · คลิกห้องเพื่อซูม · คลิก agent เพื่อดูบทสนทนา · อัปเดต ':
    ' · click a room to zoom · click an agent for the conversation · updated ',
  ' รับข้อความจากเว็บได้ (● เขียว) · ': ' can take messages from the web (● green) · ',
  ' รอคุณตอบ · คลิกบ้านเพื่อเข้าไปดู': ' waiting for you · click a house to look inside',
  'รีเซ็ตแล้ว': 'reset',
  ' quota ไม่ทราบ': ' quota unknown',
  'รายสัปดาห์': 'Weekly',
  ' วัน': 'd',
  ' ชม.': 'h',

  // the counters along the bottom
  'ว่าง / เสร็จแล้ว': 'Idle / done',
  'งานเสร็จวันนี้ (agcmd)': 'Finished today (agcmd)',
  'รอตัดสินใจ': 'Awaiting decision',
  'ขาดการเชื่อมต่อ': 'Disconnected',
  ' — ดูรายการ': ' — see the list',
  'งาน agcmd เสร็จวันนี้': 'agcmd jobs finished today',
  'รอตัดสินใจ (agcmd)': 'Awaiting decision (agcmd)',

  // list view and the panel
  'กำลังโหลดข้อความล่าสุด…': 'Loading the latest messages…',
  ' · ตอบจากเว็บได้': ' · can reply from the web',
  ' · ดูได้อย่างเดียว': ' · read only',
  '← ภาพรวม': '← Overview',
  'ไม่มีรายการ': 'Nothing here',
  'คลิกรายการเพื่อเปิดบทสนทนาแล้วพิมพ์ตอบได้ทันที': 'Click a row to open the conversation and reply straight away',
  'ไม่มีข้อความ': 'No messages',
  '— เลือกปลายทาง —': '— choose a destination —',
  'ไม่มี agent พร้อมรับงาน': 'No agent is free',
  'agent ที่กำลังทำงาน': 'Agents at work',
  ' · ไม่มี agent (shell)': ' · no agent (shell)',
  ' · ปิดอยู่': ' · off',

  // room names
  'ห้องพัฒนา': 'Development',
  'ห้องทดสอบ': 'Testing',
  'ห้องวิจัย': 'Research',
  'ห้องวางแผน': 'Planning',
  '💬 รอคุณตอบ': '💬 Waiting for you',
  'ห้องระบบ': 'Infrastructure',
  'ห้อง Deploy': 'Deploy',
  'ห้องพัก': 'Lounge',
  'ทางเข้า': 'Entrance',

  // the guide in each room
  'สวัสดี! ที่นี่ห้องวางแผน 📋': 'Hello! This is the planning room 📋',
  'agent ที่กำลังวางแผน แตกงานเป็นขั้น หรือส่งงานให้ sub-agent จะมาอยู่ที่นี่':
    'Agents planning, breaking work into steps, or handing it to a sub-agent, sit here',
  'ห้องวิจัยค่ะ 🔎': 'The research room 🔎',
  'agent ที่กำลังอ่านไฟล์ ค้นโค้ด หรือค้นข้อมูลจากเว็บ (Read · Grep · WebSearch) จะมานั่งค้นคว้าที่นี่':
    'Agents reading files, searching code or looking things up on the web (Read · Grep · WebSearch) do their digging here',
  'ห้องพัฒนา </>': 'Development </>',
  'agent ที่กำลังแก้ไฟล์โค้ดจริง (Edit · Write) จะมานั่งโต๊ะคอมที่นี่':
    'Agents editing real code files (Edit · Write) sit at the desks here',
  'ห้องทดสอบ 🧪': 'The test room 🧪',
  'agent ที่กำลังรันเทส เช่น go test, bun test, playwright จะมาที่นี่ ผ่านแล้วค่อยไปห้อง Deploy':
    'Agents running tests — go test, bun test, playwright — come here, and go on to Deploy once they pass',
  'ห้องระบบ ⚙️': 'The systems room ⚙️',
  'agent ที่กำลังรันคำสั่งทั่วไปใน shell เช่น ตรวจเครื่อง ดู log จัดการไฟล์ จะมาอยู่ที่นี่':
    'Agents running ordinary shell commands — checking the machine, reading logs, moving files — are here',
  'ห้อง Deploy 🚀': 'Deploy 🚀',
  'agent ที่กำลัง build / deploy หรือรอ Jenkins · CI อยู่ จะมานั่งที่นี่ จอจะโชว์งาน CI และสถานะล่าสุด':
    'Agents building, deploying or waiting on Jenkins · CI sit here; the screen shows the CI job and its latest state',
  'ห้องรอคุณตอบ 💬': 'The waiting room 💬',
  'agent ที่ถามคำถามหรือรอให้คุณยืนยัน จะมารอที่นี่ — คลิก agent แล้วตอบได้จากช่องด้านล่าง':
    'Agents that asked a question or need your confirmation wait here — click one and answer from the box below',
  'ห้องพัก ☕': 'The lounge ☕',
  'agent ที่ทำงานเสร็จแล้วหรือว่างรอคำสั่งใหม่ จะมาพักที่นี่':
    'Agents that have finished, or are free for new work, rest here',

  // the interior
  'ลำดับการทำงานของ Agent ตั้งแต่การวางแผนจนถึงพัก': 'The order agents work in, from planning through to rest',
  'คลิกเพื่อซูมเข้าห้อง · จอมอนิเตอร์ข้อความ': 'Click to zoom into the room · message monitor',
  'ภาพรวมการทำงาน': 'Workflow',
  'ห้องบริหาร · วางแผน': 'Management · planning',
  'กำลังโหลดข้อความ…': 'Loading messages…',
  'ไม่มี agent ในห้องนี้': 'No agent in this room',
  'คลิกเพื่อขยายจอ': 'Click to enlarge the screen',
  '⤢ ขยาย': '⤢ Enlarge',
  'ลาก agent ไปวางที่โซฟา เก้าอี้ เปล หรือจุดไหนก็ได้ · คลิก agent เพื่อเปิดแชต · คลิกจอเพื่อขยาย · ← หรือ Esc กลับ':
    'Drag an agent onto a sofa, a chair, the hammock, anywhere · click an agent to open the chat · click the screen to enlarge · ← or Esc to go back',

  // time of day
  'ดึก': 'Night',
  'เช้า': 'Morning',
  'สาย': 'Late morning',
  'บ่าย': 'Afternoon',
  'เย็น': 'Evening',

  // composer and suggestions
  '🧪 เปิดตรวจงาน: ': '🧪 Open review: ',
  'รอผล…': 'Waiting…',
  'ผู้ใช้': 'You',
  'ใช่ ': 'Yes ',
  'ใช่': 'Yes',
  'ไม่': 'No',
  'ทำต่อได้เลย': 'Carry on',
  'สรุปสถานะล่าสุดให้หน่อย': 'Summarise the latest state',
  'กด Tab เพื่อใช้': 'Tab to use',
  'คลิกเพื่อใช้': 'Click to use',
  'ไม่ระบุ session': 'No session given',

  // review
  'งานรอตรวจ (': 'Awaiting review (',
  ' · ห้องทดสอบ': ' · test room',
  'ยังไม่มีหน้าตรวจงาน — ให้ agent สร้างหน้า HTML สรุปงานแล้วรัน: aiagent review --title "…" page.html':
    'No review page yet — have the agent write an HTML summary and run: aiagent review --title "…" page.html',
  'ตรวจแล้วล่าสุด': 'Last reviewed',
  '🧪 ตรวจงาน · ': '🧪 Review · ',
  'เปิดหน้าตรวจไม่ได้: ': 'Cannot open the review page: ',
  'หมายเหตุ / สิ่งที่ต้องแก้ (ถ้าขอแก้ต้องใส่)': 'Notes / what to change (required if you ask for changes)',
  'บอกสิ่งที่ต้องแก้ก่อน': 'Say what needs changing first',
  'ส่งให้ ': 'Sent to ',
  ' แล้ว (': ' (',
  'คัดลอกข้อความตอบกลับแล้ว — วางใน session ได้เลย': 'Reply copied — paste it into the session',
  'ไม่สำเร็จ: ': 'Failed: ',
  'จาก': 'From',
  'ส่งเมื่อ': 'Sent',
  'สถานะ': 'Status',
  'รอตรวจ': 'Awaiting review',
  'ตกลงแล้ว': 'Accepted',
  'ขอแก้แล้ว': 'Changes requested',
  'โฟลเดอร์': 'Folder',
  '✅ ตกลง ปิดงาน': '✅ Accept and close',
  'คัดลอกข้อความ': 'Copy the message',
  'ส่งให้ agent': 'Send to the agent',
  '✏️ ขอแก้ไขเพิ่มเติม': '✏️ Request changes',
  'งานนี้ตรวจไปแล้ว — ส่งซ้ำไม่ได้ (คัดลอกข้อความได้)':
    'Already reviewed — it cannot be sent again (you can still copy the message)',
  'ส่งตรงไม่ได้ (session ไม่ได้รันอยู่หรือรับข้อความจากเว็บไม่ได้) — กดคัดลอกแล้ววางใน Claude / Codex เอง':
    'Cannot send directly (the session is not running, or cannot take messages from the web) — copy it and paste into Claude / Codex yourself',

  // accounts
  'อ่านสถานะบัญชีไม่ได้: ': 'Cannot read the account state: ',
  'สลับบัญชี = เปิดหน้า login ของผู้ให้บริการ (ใช้ขั้นตอนของ CLI เอง ไม่ผ่าน shell) · ทุกขั้นบันทึก audit':
    'Switching accounts opens the provider login page, through the steps the CLI itself uses, not a shell · every step is written to the audit log',
  'อีเมลบัญชีที่จะใช้ (ไม่บังคับ)': 'Account email to use (optional)',
  'วางโค้ดที่ได้หลัง login': 'Paste the code you get after signing in',
  'Codex จะ logout ทันทีที่เริ่ม (เป็นพฤติกรรมของ codex เอง) — agent Codex ทุกตัวบนเครื่องจะใช้ไม่ได้จนกว่า login ใหม่เสร็จ':
    'Codex logs out the moment this starts, which is codex behaviour and not ours — every Codex agent on this machine is unusable until the new login finishes',
  'บัญชีเดิมยังใช้ได้จนกว่าจะวางโค้ดสำเร็จ — หลังสลับ session ที่เปิดอยู่จะใช้บัญชีใหม่เมื่อรีเฟรช token':
    'The current account keeps working until the code is accepted — after the switch, open sessions move to the new account when their token refreshes',
  'สลับบัญชี ': 'Switch account ',
  'ปัจจุบัน: ': 'Currently: ',
  'เริ่ม login': 'Start login',
  ' ทุกตัวบนเครื่องนี้จะหยุดใช้งานได้จนกว่าจะ login ใหม่':
    ' on this machine will stop working until the new login finishes',
  ' แล้ว': ' done',
  'login อยู่': 'Signed in',
  'ยังไม่ได้ login': 'Not signed in',
  ' · หมดอายุ ': ' · expires ',
  '1) เปิดลิงก์นี้แล้ว sign in ด้วยบัญชีที่ต้องการ': '1) Open this link and sign in with the account you want',
  'เปิดหน้า login ↗': 'Open the login page ↗',
  'กำลังรอลิงก์…': 'Waiting for the link…',
  '2) กรอกรหัสนี้ในหน้านั้น: ': '2) Enter this code on that page: ',
  'คัดลอก': 'Copy',
  '2) วางโค้ดที่หน้าเว็บแสดง:': '2) Paste the code the page shows:',
  'ส่งโค้ดแล้ว กำลังตรวจ…': 'Code sent, checking…',
  'ยืนยันโค้ด': 'Confirm the code',
  'Codex จะเสร็จเองเมื่อกรอกรหัสบนเว็บแล้ว': 'Codex finishes by itself once the code is entered on the web',
  'ยกเลิก login': 'Cancel the login',
  'สลับบัญชี': 'Switch account',
  'ยืนยัน': 'Confirm',

  // sending work
  'ส่งไม่สำเร็จ: ': 'Could not send: ',
  'สร้างงานใหม่': 'New job',
  'ส่งข้อความถึง agent': 'Message an agent',
  'ข้อความนี้จะถูกพิมพ์เข้า agent ใน ': 'This message will be typed into the agent in ',
  ' และติดตามเป็นงาน agcmd eng': ' and tracked as an agcmd eng job',
  '\n\n+ รูปแนบ ': '\n\n+ attached ',
  ' รูป': ' image(s)',
  'ส่งเลย': 'Send',
  'ดูรูปที่แนบ': 'View the attachment',
  'รูปแนบ ': 'Attachment ',
  'อัปโหลดรูปไม่สำเร็จ: ': 'Image upload failed: ',
  'สร้างงาน ': 'Create job ',
  ' ใน ': ' in ',
  'ส่งข้อความถึง ': 'Message ',
  ' บรรทัด': ' lines',
  'ยาว': 'long',
  'ตอบ ': 'Reply to ',
  'คำตอบจะถูกพิมพ์เข้า agent ใน ': 'The answer will be typed into the agent in ',
  'ส่งคำตอบ': 'Send the answer',
  'ส่งคำตอบให้ ': 'Answer to ',

  // stopping things
  'จะรัน claude stop ': 'Will run claude stop ',
  ' — หยุด background session นี้ บทสนทนายังเก็บไว้ เปิดต่อได้ด้วย claude attach ':
    ' — stops this background session. The conversation is kept, and can be reopened with claude attach ',
  'จะรัน agcmd restart ': 'Will run agcmd restart ',
  ' — ปิดโปรแกรม agent ในหน้าต่าง tmux ของ workspace นี้ทันที งานที่ทำค้างจะหยุด':
    ' — closes the agent program in this workspace tmux window at once. Work in progress stops',
  '\nหมายเหตุ: agent นี้คือตัวที่รับงานจากเว็บผ่าน ': '\nNote: this is the agent that takes web jobs through ',
  ' หยุดแล้วจะสั่งงานจากเว็บไม่ได้จนกว่าจะเปิดใหม่':
    ' — once stopped, the web cannot send it work until it is started again',
  'หยุด ': 'Stop ',
  'หยุด agent': 'Stop the agent',
  'อนุมัติ': 'Approve',
  'ยกเลิก': 'Cancel',
  'เปลี่ยนสถานะงานใน agcmd เป็น ': 'Sets the agcmd job state to ',
  ' (ไม่ปิด process ของ agent)': ' (does not kill the agent process)',
  '📍 ติดตาม ': '📍 Following ',

  // chrome
  'ซ่อนแผงขวา ⟩': 'Hide the panel ⟩',
  '⟨ แสดงแผง': '⟨ Show the panel',
  'เต็มจอ (F)': 'Full screen (F)',
  'สลับเต็มจอ': 'Toggle full screen',
  'ลากเพื่อปรับความกว้างแผงขวา': 'Drag to resize the panel',
  'ลากเพื่อปรับความกว้าง · ดับเบิลคลิกเพื่อรีเซ็ต': 'Drag to resize · double-click to reset',

  // index.html — the static shell
  'มุมมอง': 'View',
  'หมู่บ้าน': 'Village',
  'รายการ': 'List',
  'กำลังเชื่อมต่อ…': 'Connecting…',
  'บัญชี Claude / Codex บนเครื่องนี้': 'Claude / Codex accounts on this machine',
  '👤 บัญชี': '👤 Accounts',
  'เปิดแผงขวาเป็นหน้าต่างใหญ่': 'Open the panel as a large window',
  '⤢ ขยายแผง': '⤢ Expand the panel',
  'ออกจากระบบในแท็บนี้': 'Sign out of this tab',
  'ออก': 'Sign out',
  'กลับหมู่บ้าน': 'Back to the village',
  'ซูมออก': 'Zoom out',
  'ซูมเข้า': 'Zoom in',
  'เต็มจอ': 'Full screen',
  'หมู่บ้าน agent': 'agent village',
  'รายละเอียด': 'Details',
  'ส่งไปที่ workspace': 'Send to workspace',
  'ชนิดคำสั่ง': 'Command type',
  'งานใหม่ (ติดตามได้)': 'New job (tracked)',
  'ข้อความถึง agent': 'Message to an agent',
  'แนบรูป (สูงสุด 10)': 'Attach images (max 10)',
  'แนบรูป': 'Attach images',
  'พิมพ์สั่งงานเป็นภาษาไทยได้เลย…': 'Type an instruction — Thai or English…',
  'ข้อความ': 'Message',
  'ส่ง': 'Send',
  'จอ': 'Screen',
  'ปิด': 'Close',
  'บัญชี AI บนเครื่องนี้': 'AI accounts on this machine',
  'ตรวจงาน': 'Review',
  'หน้าตรวจงาน': 'Review page',
  'รูปขยาย': 'Enlarged image',
  'เข้าสู่ AI Agent World': 'Sign in to AI Agent World',
  'ใส่ token ของ AI Agent World (ขึ้นต้นด้วย ': 'Enter the AI Agent World token (it starts with ',
  ') — ได้จาก ': ') — from ',
  ' หรือ ': ' or ',
  ' บนเครื่องนี้ · เก็บไว้เฉพาะแท็บนี้': ' on this machine · it is kept to this tab only',
  'เข้าใช้งาน': 'Sign in',
  'จำไว้ในเครื่องนี้ (สำหรับมือถือ / แอปหน้าจอหลัก)': 'Remember on this device (for a phone or a home-screen app)',

  // composer target
  'ส่งถึง: ': 'To: ',
  'เปลี่ยนเป็นส่งตาม workspace': 'Switch to sending by workspace',
  'พิมพ์ถึง ': 'Typing to ',
  '… (Enter ส่ง · Shift+Enter ขึ้นบรรทัด · วาง/ลากรูปได้)':
    '… (Enter to send · Shift+Enter for a new line · paste or drag images)',
  '… (Enter ส่ง · Shift+Enter ขึ้นบรรทัด)': '… (Enter to send · Shift+Enter for a new line)',
  'ส่งถึง ': 'To ',
  ' ใน tmux ': ' in tmux ',
  'เลือกปลายทางก่อน หรือคลิกตัวละครที่รับงานได้': 'Choose a destination first, or click an agent that can take work',

  // the houses on the map
  'บ้าน agcmd': 'The agcmd house',
  'tmux · สั่งงานจากเว็บได้': 'tmux · can be given work from the web',
  'บ้าน Background': 'The Background house',
  'Claude daemon · ส่งข้อความผ่าน relay': 'Claude daemon · messages go through the relay',
  'บ้าน Terminal · VS Code': 'The Terminal · VS Code house',
  'เปิดในเครื่องของผู้ใช้ · ดูได้อย่างเดียว': 'Open on your own machine · read only',
  'บ้าน Deploy': 'The Deploy house',
  'กำลัง Build และ Deploy': 'Building and deploying',
  'บ้าน Infra': 'The Infra house',
  'ตรวจสอบสถานะระบบ': 'Checking system state',

  // weather and CI
  'รอ CI: ': 'Waiting on CI: ',
  'รอคุณตรวจงาน (': 'Awaiting your review (',
  'ว่าง': 'Idle',
  'กำลังช่วย': 'Helping',
  'อากาศตาม quota: ใช้ไปสูงสุด ': 'Weather follows the quota: highest use ',
  'แดดใส': 'Clear',
  'เมฆมาก': 'Cloudy',
  'พายุ': 'Storm',
  '🚀 CI ผ่าน: ': '🚀 CI passed: ',
  '💨 CI ไม่ผ่าน: ': '💨 CI failed: ',
  ' รอคุณตอบ': ' waiting for you',
  ' กำลังทำงาน': ' working',
  'ว่างทั้งหมด': 'All idle',
  'ไม่มี agent': 'No agents',

  // what each room is for
  'กำหนดเป้าหมาย วางแผนงาน และแบ่งงาน': 'Setting goals, planning and splitting the work',
  'ค้นคว้า รวบรวมข้อมูล และวิเคราะห์': 'Researching, gathering and analysing',
  'พัฒนาและปรับปรุงตามแผน': 'Building and refining to the plan',
  'ทดสอบการทำงาน ตรวจสอบคุณภาพ': 'Testing behaviour and checking quality',
  'ดูแลระบบ สภาพแวดล้อม และทรัพยากร': 'Looking after systems, environments and resources',
  'เตรียม Build และ Deploy ขึ้นใช้งาน': 'Preparing the build and the deployment',
  'รอคำตอบหรือการยืนยันจากคุณ': 'Waiting for your answer or confirmation',
  'พักและเตรียมพร้อมสำหรับงานถัดไป': 'Resting and getting ready for the next job',

  // the stepper, where room names are short
  'วางแผน': 'Plan',
  'วิจัย': 'Research',
  'พัฒนา': 'Code',
  'ทดสอบ': 'Test',
  'ระบบ': 'Ops',
  'พัก': 'Lounge',

  // list view
  'Agent ที่ออนไลน์ (': 'Agents online (',
  'ชื่อ': 'Name',
  'ชนิด': 'Kind',
  'งาน': 'Job',
  'ล่าสุด': 'Latest',
  'งาน agcmd (': 'agcmd jobs (',
  'Session ที่จบแล้ววันนี้ (': 'Sessions finished today (',

  // the detail panel
  'agent นี้': 'this agent',
  ' ไม่ได้รันใน tmux ของ agcmd — รับงานจากเว็บไม่ได้ (ดูได้อย่างเดียว)':
    ' is not running in an agcmd tmux window — it cannot take work from the web (read only)',
  'Sub-agent · ดูได้อย่างเดียว': 'Sub-agent · read only',
  'กำลังทำ · ': 'Working · ',
  'เครื่องมือล่าสุด': 'Latest tool',
  'อัปเดต': 'Updated',
  'agent แม่': 'Parent agent',
  'งานที่ได้รับจาก agent แม่': 'Job given by the parent agent',
  'รายงานล่าสุด': 'Latest report',
  'sub-agent ทำงานแทน agent แม่ และรายงานผลกลับไปที่ agent แม่ — สั่งงานแทรกไม่ได้ ถ้าจะสั่งให้พิมพ์ถึง agent แม่':
    'A sub-agent works on behalf of its parent and reports back to it — you cannot interrupt it with an order; to direct it, write to the parent agent',
  'ไปที่ agent แม่: ': 'Go to the parent agent: ',
  ' · อัปเดต ': ' · updated ',
  'งาน: ': 'Job: ',
  'ล่าสุด: ': 'Latest: ',
  'Sub-agents ที่ช่วยอยู่ (': 'Sub-agents helping (',
  ') · ดูได้อย่างเดียว': ') · read only',
  'เปลี่ยนชื่อที่แสดงในเว็บ': 'Rename it for the web',
  'เปลี่ยนชื่อ': 'Rename',
  '🧪 รอคุณตรวจงาน': '🧪 Awaiting your review',
  'เปิดตรวจ →': 'Open the review →',
  'บทสนทนา': 'Conversation',
  'ไม่ได้นับ': 'not counted',
  ' เข้า · ': ' in · ',
  ' ออก · cache ': ' out · cache ',
  ' (กำลังนับ… ยังไม่ครบ)': ' (still counting…)',
  'ได้ — ผ่าน agcmd workspace ': 'Yes — through agcmd workspace ',
  'ได้ — ผ่าน relay (tmux aiagent-': 'Yes — through the relay (tmux aiagent-',
  ' ที่รัน claude attach)': ' running claude attach)',
  'ไม่ได้ — ': 'No — ',
  'ไม่มีช่องทางที่ปลอดภัย': 'no safe channel',
  'เจ้าของ': 'Owner',
  'เริ่ม': 'Started',
  'เหตุการณ์ล่าสุด': 'Latest event',
  'หลักฐาน': 'Evidence',
  'ส่งข้อความจากเว็บ': 'Message from the web',
  'ดูในเครื่อง': 'View on the machine',
  'หยุด agent (claude stop)': 'Stop the agent (claude stop)',
  'หยุด agent (agcmd restart)': 'Stop the agent (agcmd restart)',
  'หยุดจากเว็บไม่ได้: ': 'Cannot stop it from the web: ',
  'ลำดับงานล่าสุด (ชื่อเครื่องมือเท่านั้น)': 'Recent steps (tool names only)',
  'กำลังโหลดลำดับงาน…': 'Loading the steps…',
  'เปิดอยู่ในเทอร์มินัล/VS Code ของผู้ใช้': 'Open in your terminal / VS Code',
  'คัดลอกแล้ว': 'Copied',
  'ที่มาของสถานะ': 'Where the state comes from',
  'agent/ผู้ใช้รายงานผ่าน agcmd (ไม่ได้วัดจาก process)':
    'reported through agcmd by the agent or by you, not measured from the process',
  'สร้าง': 'Created',
  ' · เปิดอยู่': ' · open',
  ' · ไม่พบ session': ' · no session found',

  // conversation
  'กำลังโหลดบทสนทนา…': 'Loading the conversation…',
  'พิมพ์ตอบที่ช่องยาวด้านล่าง (ส่งถึง agent ตัวนี้) · แนบรูปได้ · ':
    'Reply in the long box below — it goes to this agent · images can be attached · ',
  'ผ่าน relay claude attach ': 'through the relay, claude attach ',
  'ผ่าน agcmd dispatch ': 'through agcmd dispatch ',
  'ส่งข้อความจากเว็บไม่ได้: ': 'Cannot message it from the web: ',
  'แสดงเฉพาะข้อความผู้ใช้และคำตอบของ agent · ข้อมูลลับ (token, key, รหัสผ่าน) ถูกปิดบังก่อนส่งมาที่เว็บ':
    'Only your messages and the agent replies are shown · secrets (tokens, keys, passwords) are redacted before they reach the web',
  'ยังไม่มีข้อความ': 'No messages yet',
  'โหลดบทสนทนาไม่ได้: ': 'Cannot load the conversation: ',

  // attachments
  'ดู/จัดการรูปแนบ': 'View / manage attachments',
  'รูปแนบ': 'Attachments',
  'คลิกรูปเพื่อดูใหญ่': 'Click an image to enlarge it',
  '+ เพิ่มรูป': '+ Add an image',
  'คลิกเพื่อดูภาพใหญ่': 'Click to enlarge',
  'ลบรูป ': 'Remove image ',
  ' พร้อมรูป ': ' with ',

  // sending, confirmations
  'บริการนี้จะเปิด claude attach ': 'This service will open claude attach ',
  ' ใน tmux ส่วนตัวแล้วพิมพ์ข้อความนี้ให้ (เหมือนคุณพิมพ์เอง)':
    ' in a private tmux and type this message for you, as though you had typed it',
  'ส่งผ่าน agcmd dispatch ': 'Sent through agcmd dispatch ',
  ' · ยืนยันครั้งเดียวต่อ agent ในแท็บนี้': ' · confirmed once per agent in this tab',
  'พิมพ์คำตอบ…': 'Type your answer…',
  'คำตอบ': 'Answer',
  'ตอบ': 'Answer',
  'อนุมัติ (ตั้งเป็น READY)': 'Approve (set to READY)',
  'ยกเลิกงาน': 'Cancel the job',
  'อนุมัติ/ยกเลิก เปลี่ยนสถานะใน agcmd เท่านั้น — ไม่ merge, ไม่ deploy และไม่ปิด process':
    'Approve and cancel change the state in agcmd and nothing else — no merge, no deploy, no process killed',
  'บันทึกของงาน': 'Job notes',
  '(ว่าง)': '(empty)',
  'กำลังโหลด…': 'Loading…',
  'ตอบจากเว็บได้ · ': 'Can reply from the web · ',
  'ดูได้อย่างเดียว · ': 'Read only · ',
  ' ขาดการเชื่อมต่อ': ' disconnected',
  'สถานะ RUNNING แต่ไม่พบ tmux ': 'State is RUNNING but no tmux ',
  'ใช้ไป ': 'used ',
  'รอบ': 'cycle',
  ' · อ่านเมื่อ ': ' · read ',

  // today / energy
  'ภาพรวมวันนี้': 'Today',
  'ต้องให้คุณช่วย (': 'Needs you (',
  'ไม่มีงานที่รอคุณ': 'Nothing is waiting for you',
  'พลังงาน AI': 'AI energy',
  'ตัวเลข quota มาจากผู้ให้บริการเท่านั้น ถ้าไม่มีข้อมูลจะแสดง “ไม่ทราบ” ไม่ประมาณเอง':
    'Quota figures come from the provider and nowhere else; with no figure it says Unknown rather than guessing',
  'วิธีใช้': 'How to use it',
  'คลิกตัวละครหรือแถวในมุมมองรายการเพื่อดูรายละเอียด · ช่องด้านล่างใช้สั่งงานไปยัง agent ใน workspace ของ agcmd':
    'Click an agent, or a row in the list view, for the detail · the box below sends work to an agent in an agcmd workspace',
  'เปิดการอ่าน % ของ Claude': 'Turn on the Claude % reading',
  'Claude Code ส่งตัวเลข % ให้เฉพาะ status line — ตั้งครั้งเดียวใน ~/.claude/settings.json:':
    'Claude Code gives the % only to a status line — set it once in ~/.claude/settings.json:',
  'ตัวเลขอัปเดตเมื่อมี Claude session ที่เปิดหน้าจออยู่อย่างน้อยหนึ่งตัว':
    'The figure updates while at least one Claude session has its screen open',
  'Claude ใช้ไป ': 'Claude has used ',

  // model switching
  'แนะนำลด agent ที่ใช้ ': 'Suggest moving agents on ',
  ' เป็น Sonnet เพื่อยืดโควตา:': ' to Sonnet, to stretch the quota:',
  'ไม่มี agent ที่สั่งได้ซึ่งใช้ model ใหญ่อยู่': 'No agent you can direct is on a large model',
  'เปลี่ยน model': 'Change the model',
  'จะพิมพ์ /model ': 'Will type /model ',
  ' เข้า ': ' into ',
  ') — มีผลกับข้อความถัดไปของ agent ตัวนี้': ') — it takes effect from this agent next message',
  'เปลี่ยนเลย': 'Change it',
  'เปลี่ยน ': 'Change ',
  ' เป็น ': ' to ',
  'model เริ่มต้นของ Codex': 'Codex default model',
  'เปลี่ยน model เริ่มต้นของ Codex': 'Change the Codex default model',
  'Codex ที่รันอยู่เปลี่ยนจากภายนอกไม่ได้ — ค่านี้มีผลกับ session ใหม่ (~/.codex/config.toml)':
    'A running Codex cannot be changed from outside — this applies to new sessions (~/.codex/config.toml)',
  'เป็น ': 'to ',
  'เปลี่ยน': 'Change',
  'Codex session ใหม่จะใช้ ': 'New Codex sessions will use ',
  'model เริ่มต้น: ': 'Default model: ',
  'เปลี่ยน model…': 'Change the model…',
  'เปลี่ยนได้เฉพาะ agent ที่รับข้อความจากเว็บ': 'Only agents that take web messages can be changed',

  // automatic model drop
  '⚙️ ลด model อัตโนมัติเมื่อ quota เกิน': '⚙️ Drop the model automatically when the quota runs high',
  'เปิดลด model อัตโนมัติที่ ': 'Automatic model drop on at ',
  'ปิดลด model อัตโนมัติแล้ว (จะคืน model เดิมให้)': 'Automatic model drop off (the old model is restored)',
  'บันทึกไม่ได้: ': 'Cannot save: ',
  'เปิดใช้': 'On',
  'เมื่อใช้ไปเกิน': 'When use passes',
  'Codex (session ใหม่) →': 'Codex (new sessions) →',
  'ตรวจทุก 1 นาที · คืน model เดิมเมื่อใช้ลดลงต่ำกว่าเกณฑ์ 15 จุด หรือรอบรีเซ็ต · บันทึก audit ทุกครั้ง':
    'Checked every minute · the old model comes back when use falls 15 points below the threshold, or at the reset · every change is written to the audit log',
  'บันทึก': 'Save',

  // idle work
  'เปิดงานช่วงว่าง: ว่างเกิน ': 'Idle work on: idle over ',
  ' นาที และ quota ต่ำกว่า ': ' minutes and quota below ',
  'ปิดงานช่วงว่างแล้ว': 'Idle work off',
  '💡 ใช้ช่วงว่างคิดงาน (เสนอก่อน รออนุมัติ)': '💡 Use idle time to think (it proposes, you approve)',
  'ว่างเกิน': 'Idle over',
  'นาที': 'minutes',
  'และ quota ต่ำกว่า': 'and quota below',
  'ให้ agent ใน workspace': 'Agent in workspace',
  'agent จะคิดไอเดียเกม asset และจุดเสี่ยง/ช้า (ปรึกษา Codex แบบอ่านอย่างเดียวได้) แล้วส่งเป็นหน้าตรวจงานในห้องทดสอบ — ห้ามแก้โค้ดจนกว่าคุณอนุมัติ · สูงสุด 1 ครั้งต่อ 6 ชม. และไม่ส่งซ้ำถ้ายังมีข้อเสนอรอตรวจ':
    'The agent thinks up game, asset and risk or slowness ideas — it may consult Codex read-only — and sends them as a review page in the test room. It must not touch code until you approve · at most once every 6 hours, and never again while a proposal is still awaiting review',
  'ส่งล่าสุด ': 'Last sent ',

  // renaming a session
  'ตั้งชื่อ session นี้\n': 'Name this session\n',
  'ถ้า agent รับข้อความจากเว็บได้ จะส่ง /rename เข้าไปให้ ชื่อใน CLI เปลี่ยนตามด้วย\n':
    'If the agent takes messages from the web, /rename is sent to it and the CLI name changes too\n',
  'เปลี่ยนชื่อใน CLI แล้ว — ถ้า agent กำลังทำงานอยู่ จะมีผลเมื่อรอบนี้จบ':
    'Renamed in the CLI — if the agent is mid-task it takes effect when that finishes',
  'เปลี่ยนเฉพาะในเว็บ — ส่งเข้า CLI ไม่ได้ (agent รับข้อความจากเว็บไม่ได้)':
    'Changed on the web only — it could not be sent to the CLI (this agent cannot take messages from the web)',
  'เว้นว่างเพื่อกลับไปใช้ชื่อเดิม': 'Leave it blank to go back to the original name',
  'เปลี่ยนชื่อไม่ได้: ': 'Cannot rename: '
}

var LANG = 'th'
try {
  var saved = localStorage.getItem(LANG_KEY)
  if (saved === 'en' || saved === 'th') LANG = saved
} catch (e) { /* storage unavailable: the language lives in memory only */ }

// ?lang=en wins over the stored choice and replaces it, so a link can carry
// the language to someone who has never opened the page. It is also the only
// way to screenshot a given language without a browser you can script.
var q = /[?&]lang=(en|th)\b/.exec(location.search)
if (q) {
  LANG = q[1]
  try { localStorage.setItem(LANG_KEY, LANG) } catch (e) { /* see above */ }
}

// tr, not t: `t` is a natural name for a loop variable and app.js already
// binds it twice (a timeline entry, an API result). A shadowed translator
// throws "t is not a function" on whichever branch happens to run.
function tr (s) {
  if (LANG !== 'en') return s
  var v = EN[s]
  return v === undefined ? s : v
}

function currentLang () { return LANG }

// ----- the static shell -----
// Captured once, before app.js runs, so the originals are the Thai that
// shipped in index.html rather than whatever is on screen at the time.
var staticText = []  // [textNode, thai]
var staticAttr = []  // [element, attribute, thai]
var THAI = /[฀-๿]/

function captureStatic (root) {
  var walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  var n
  while ((n = walker.nextNode())) {
    if (THAI.test(n.nodeValue)) staticText.push([n, n.nodeValue])
  }
  var attrs = ['title', 'aria-label', 'placeholder']
  var all = root.querySelectorAll('*')
  for (var i = 0; i < all.length; i++) {
    for (var j = 0; j < attrs.length; j++) {
      var v = all[i].getAttribute(attrs[j])
      if (v && THAI.test(v)) staticAttr.push([all[i], attrs[j], v])
    }
  }
}

function applyStaticLang () {
  for (var i = 0; i < staticText.length; i++) {
    staticText[i][0].nodeValue = tr(staticText[i][1])
  }
  for (var k = 0; k < staticAttr.length; k++) {
    staticAttr[k][0].setAttribute(staticAttr[k][1], tr(staticAttr[k][2]))
  }
  document.documentElement.lang = LANG
}

// setLang persists the choice and reloads.
//
// A reload rather than a repaint, because app.js builds its label tables —
// statusLabel, activityLabel, roomDefs, npcs and the rest — at module scope,
// so the Thai in them is translated once when the script runs and a later
// render() would hand back the same frozen strings. Translating at every
// lookup instead would work, but it means finding every one of those tables
// and never missing one in future; a reload is correct without that promise.
// The token and the language both live in localStorage, so nothing is lost.
function setLang (l) {
  if (l !== 'en' && l !== 'th') return
  try { localStorage.setItem(LANG_KEY, l) } catch (e) { /* see above */ }
  LANG = l
  location.reload()
}

captureStatic(document.body)
applyStaticLang()
