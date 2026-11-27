'use strict'
// AI Agent World — read live snapshots, draw the village, send work via
// the token-gated API. Every value from the server goes through textContent,
// nothing is ever injected as HTML.

const tokenKey = 'aiagentToken'
const roleKey = 'aiagentRole'
const pollMs = 4000
const staleMs = 12000
const ns = 'http://www.w3.org/2000/svg'

const st = {
  token: safeGet(tokenKey),
  snap: null,
  lastOk: 0,
  // the list is home: it is the view that holds any number of sessions
  view: 'list',
  sel: null,
  detail: null,
  runLog: null,
  timer: null
}

// The token lives in sessionStorage by default, so closing the tab forgets
// it. That is the right default on a shared desktop and the wrong one for a
// home-screen PWA, which gets a fresh session on every cold start and would
// ask for the token every time you glanced at your phone. So "remember on
// this device" is offered at login and, when ticked, uses localStorage.
// Reading checks both; logging out clears both.
function safeGet (k) {
  try { return sessionStorage.getItem(k) || localStorage.getItem(k) || '' } catch (e) { return '' }
}
function safeSet (k, v, persist) {
  try {
    sessionStorage.removeItem(k)
    localStorage.removeItem(k)
    if (v) (persist ? localStorage : sessionStorage).setItem(k, v)
  } catch (e) { /* storage unavailable: token lives in memory only */ }
}

const $ = id => document.getElementById(id)
function h (tag, attrs, ...kids) {
  const e = document.createElement(tag)
  for (const k in (attrs || {})) {
    if (k === 'class') e.className = attrs[k]
    else if (k.startsWith('on')) e.addEventListener(k.slice(2), attrs[k])
    else if (attrs[k] !== undefined && attrs[k] !== null && attrs[k] !== false) e.setAttribute(k, attrs[k])
  }
  for (const kid of kids.flat()) {
    if (kid === null || kid === undefined || kid === false) continue
    e.append(kid instanceof Node ? kid : document.createTextNode(String(kid)))
  }
  return e
}
function s (tag, attrs, parent) {
  const e = document.createElementNS(ns, tag)
  for (const k in attrs) e.setAttribute(k, attrs[k])
  if (parent) parent.appendChild(e)
  return e
}

// ---------- labels ----------
const activityLabel = {
  codeEdit: tr('กำลังเขียนโค้ด'),
  testRun: tr('กำลังทดสอบ'),
  deploy: tr('กำลัง Deploy'),
  research: tr('กำลังอ่าน/วิเคราะห์'),
  command: tr('กำลังรันคำสั่ง'),
  planning: tr('กำลังวางแผน'),
  idle: tr('ว่าง รอคำสั่ง'),
  unknown: tr('กำลังทำงาน')
}
const statusLabel = { running: tr('ทำงานอยู่'), idle: tr('ว่าง รอคำสั่ง'), ended: tr('จบ session แล้ว'), needsInput: tr('รอคุณตอบ'), completed: tr('เสร็จแล้ว') }
function stateText (sess) {
  if (!sess.live) return statusLabel.ended
  if (sess.status === 'running') return activityLabel[sess.activityType] || statusLabel.running
  return statusLabel[sess.status] || sess.status
}
const runLabel = {
  RUNNING: tr('กำลังทำ'),
  WAITING_DECISION: tr('รอคุณตัดสินใจ'),
  READY: tr('เสร็จ รอตรวจ'),
  DONE: tr('เสร็จแล้ว'),
  PAUSED: tr('พัก (บันทึกไว้)'),
  ABORTED: tr('ยกเลิกแล้ว')
}
const modeLabel = { normal: tr('ปกติ'), eco: tr('ประหยัด'), critical: tr('วิกฤต'), reserve: tr('สำรองไว้'), guarded: tr('ไม่ทราบ') }
const providerName = { openai: 'Codex', anthropic: 'Claude', google: 'Antigravity' }
const runtimeName = { claudeCode: 'Claude Code', codexCli: 'Codex CLI', agyCli: 'Antigravity' }
const evidenceLabel = {
  'pid+procStart': tr('ยืนยันจาก process (pid + เวลาเริ่ม)'),
  openFileHandle: tr('ยืนยันจาก process ที่เปิดไฟล์ session'),
  processCwd: tr('มี process codex ในโฟลเดอร์เดียวกัน'),
  none: tr('ไม่มี process ทำงานอยู่')
}

function fmtTime (iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d)) return '—'
  return d.toLocaleTimeString('th-TH', { hour: '2-digit', minute: '2-digit' })
}
function ago (iso) {
  if (!iso) return '—'
  const sec = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000))
  if (sec < 60) return sec + tr(' วินาทีที่แล้ว')
  if (sec < 3600) return Math.round(sec / 60) + tr(' นาทีที่แล้ว')
  if (sec < 86400) return Math.round(sec / 3600) + tr(' ชั่วโมงที่แล้ว')
  return Math.round(sec / 86400) + tr(' วันที่แล้ว')
}
function num (n) {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M'
  if (n >= 1e3) return Math.round(n / 1e3) + 'k'
  return String(n)
}
function shortId (id) { return id && id.length > 14 ? id.slice(0, 8) + '…' + id.slice(-4) : (id || '—') }
function agentColor (sess) { return sess.runtimeType === 'codexCli' ? 'var(--codex)' : 'var(--claude)' }
function agentHex (sess) { return sess.runtimeType === 'codexCli' ? '#5CC8F0' : '#E48D62' }

// ---------- api ----------
// The menu starts without the admin's entries and opens when the role is known,
// so a user never sees them flash. An admin's last role is remembered beside
// the token, so a refresh does not make the admin wait either. It only paints
// the menu: the server decides every request by the token.
function safeLocal (k) { try { return !!localStorage.getItem(k) } catch (e) { return false } }
// Users know a protected session only as "admin"; the admin sees the same word
// first, then its own name.
function nameOf (sess) {
  return sess.protected && !isUser() ? 'admin · ' + sess.displayName.replace(/\s*\(admin\)$/, '') : sess.displayName
}
function showMenu (role) {
  for (const id of ['viewPM', 'viewTasks', 'accountsBtn', 'releaseBtn']) $(id).hidden = role !== 'admin'
  if (role === 'admin') checkRelease()
}
// a vibe user: own agents only, no PM, no task board (the server refuses both)
function isUser () { return !!st.me && st.me.role === 'user' }
// st.as: the user whose workspace an admin is looking at
function authHeaders () { return st.as ? { 'X-Aiagent-Token': st.token, 'X-Aiagent-As': st.as } : { 'X-Aiagent-Token': st.token } }
function viewAs (name) {
  st.as = name
  st.snap = null; st.accSig = ''; st.models = null; st.sel = null
  $('asBar').hidden = !name
  $('asName').textContent = name
  showPreview()
  $('accountsDlg').close()
  poll()
}
// A room's page, on its own host: a user's own room, or the one an admin is in.
function previewRoom () { return !st.me || !st.me.preview ? '' : isUser() ? st.me.osUser : st.as }
// The room a session's preview belongs to: a room's own session, when
// previews are on. The admin's own sessions have no room page.
function sessPreviewRoom (sess) {
  // a room's session carries its OS user as both owner and repoId
  if (!st.me || !st.me.preview || !sess || !sess.owner || sess.owner === 'root' || sess.repoId !== sess.owner) return ''
  return isUser() ? st.me.osUser : sess.owner
}
function showPreview () { $('previewBtn').hidden = !previewRoom() }
async function openPreview (room) {
  // opened before the request, or the browser takes it for a popup
  const w = window.open('', '_blank')
  if (w) w.opener = null
  try {
    const { url } = await api('/preview/ticket', { method: 'POST', body: { room } })
    const to = url + '&l=' + LANG
    if (!w) { previewLink(room, to); return }
    w.location.replace(to)
    // Some browsers leave the pre-opened tab blank instead of navigating it
    // (seen on Chrome 157: a ticket was issued and the room was never
    // loaded). A tab still on about:blank is ours to read; then hand the
    // same one-use link over as a plain link the user clicks.
    setTimeout(() => {
      let blank = false
      try { blank = !w.closed && w.location.href === 'about:blank' } catch (e) { /* it navigated away: fine */ }
      if (blank) { try { w.close() } catch (e) { /* fine */ } previewLink(room, to) }
    }, 2500)
  } catch (e) { if (w) w.close(); toast(tr('ไม่สำเร็จ: ') + e.message, true) }
}
// previewLink offers the one-use preview link as a link to click, for when
// the tab could not be opened or navigated for the user.
function previewLink (room, to) {
  const dlg = h('dialog', { class: 'dlg previewLinkDlg' })
  const close = () => { dlg.close(); dlg.remove() }
  dlg.append(
    h('p', null, tr('เปิดแท็บพรีวิวให้อัตโนมัติไม่ได้ กดลิงก์นี้เพื่อเปิดหน้าของห้อง ') + room + tr(' (ใช้ได้ครั้งเดียว ภายใน 1 นาที)')),
    h('div', { class: 'dlgBtns' },
      h('a', { href: to, target: '_blank', rel: 'noopener noreferrer', class: 'btnLink pri', onclick: () => setTimeout(close, 0) }, tr('🖥 เปิดพรีวิว')),
      h('button', { type: 'button', class: 'ghost', onclick: close }, tr('ปิด'))))
  document.body.append(dlg)
  dlg.showModal()
}
async function api (path, opts) {
  const res = await fetch('/aiagent/api' + path, {
    method: (opts && opts.method) || 'GET',
    headers: Object.assign(authHeaders(), opts && opts.body ? { 'Content-Type': 'application/json' } : {}),
    body: opts && opts.body ? JSON.stringify(opts.body) : undefined,
    cache: 'no-store'
  })
  let body = null
  try { body = await res.json() } catch (e) { body = null }
  // Two different 429s: AIAGENT_LOCKED is wrong tokens and asks for one again.
  // AIAGENT_SLOW_DOWN is two writes inside a second — the caller reports it
  // and the user stays signed in.
  const slow = res.status === 429 && body && body.code === 'AIAGENT_SLOW_DOWN'
  if (res.status === 401 || (res.status === 429 && !slow)) {
    showLogin(body && serverText(body.message))
    throw new Error('auth')
  }
  if (!res.ok) {
    const err = new Error((body && serverText(body.message)) || ('HTTP ' + res.status))
    err.code = body && body.code
    throw err
  }
  return body ? body.details : null
}

// ---------- polling ----------
async function poll () {
  clearTimeout(st.timer)
  if (!st.token) { showLogin(); return }
  try {
    if (st.meFor !== st.token) {
      st.as = ''; $('asBar').hidden = true
      st.me = await api('/me')
      st.meFor = st.token
      showMenu(st.me.role)
      showPreview()
      safeSet(roleKey, st.me.role, safeLocal(tokenKey))
      if (isUser() && (st.view === 'pm' || st.view === 'tasks')) setView('list')
    }
    st.snap = await api('/snapshot')
    st.lastOk = Date.now()
    if (!st.revAt || Date.now() - st.revAt > 10000) {
      st.revAt = Date.now()
      try { st.reviews = (await api('/reviews')).reviews || [] } catch (e) { /* keep */ }
    }
    await loadBoard()
    render()
  } catch (e) {
    if (e.message === 'auth') return
    // Anything that is not an auth failure is a bug in here, not a network
    // problem, and swallowing it is how a null `runs` turned into a blank
    // list view that reported nothing for weeks. A broken render must still
    // say so, and must not stop the next poll.
    if (!(e instanceof Error) || !/HTTP \d|fetch|NetworkError|Failed to fetch/i.test(e.message)) {
      console.error('render failed:', e)
    }
  }
  renderConn()
  // a hidden tab polls slowly (less work for the box and the browser)
  st.timer = setTimeout(poll, document.hidden ? 30000 : pollMs)
}
document.addEventListener('visibilitychange', () => { if (!document.hidden && st.token) poll() })

function renderConn () {
  const c = $('conn')
  const fresh = st.lastOk && Date.now() - st.lastOk < staleMs
  $('stage').classList.toggle('stale', !fresh)
  if (fresh) {
    // Connected is the uninteresting case, and the clock next to it repeated
    // the one on the phase chip just below. A green dot is the whole message;
    // the exact time stays in the title for anyone who wants it.
    c.className = 'conn ok live'
    c.textContent = ''
    c.title = tr('สด · ') + fmtTime(st.snap && st.snap.generatedAt)
  } else {
    c.className = 'conn bad'
    c.title = ''
    c.textContent = st.lastOk ? tr('ขาดการเชื่อมต่อ · ข้อมูลล่าสุด ') + fmtTime(new Date(st.lastOk).toISOString()) : tr('ยังเชื่อมต่อไม่ได้')
  }
  const b = $('banner')
  if (!fresh && st.lastOk) {
    b.hidden = false
    b.className = 'banner bad'
    b.textContent = tr('ขาดการเชื่อมต่อกับบริการ — ภาพด้านล่างเป็นข้อมูลเก่า ไม่ใช่สถานะปัจจุบัน')
  } else if (st.snap) {
    renderBanner()
  }
}

function renderBanner () {
  const b = $('banner')
  const q = (st.snap.quotas || []).filter(x => x.confidence === 'actual')
  const worst = q.find(x => x.mode === 'reserve') || q.find(x => x.mode === 'critical') || q.find(x => x.mode === 'eco')
  const warn = (st.snap.warnings || [])[0]
  if (worst) {
    b.hidden = false
    b.className = 'banner'
    b.textContent = tr('AI กำลังประหยัดพลังงาน — ') + providerName[worst.providerId] + tr(' ใช้ไป ') + Math.round(worst.usedPercent) + '% (' + modeLabel[worst.mode] + ')' + (worst.resetsAt ? tr(' · รีเซ็ต ') + new Date(worst.resetsAt).toLocaleString('th-TH', { dateStyle: 'short', timeStyle: 'short' }) : '')
  } else if (warn) {
    b.hidden = false
    b.className = 'banner'
    b.textContent = warn
  } else {
    b.hidden = true
  }
}

// ---------- render ----------
function render () {
  const snap = st.snap
  $('host').textContent = (snap.host || '') + (snap.version ? ' · v' + snap.version : '')
  const live = snap.sessions.filter(x => x.live)
  const reach = live.filter(x => x.dispatchVia).length
  const hs = st.house ? { name: tr('บ้าน Agent'), sub: live.length + tr(' agent บนเครื่องนี้') } : null
  const rf = hs && st.roomFocus && roomTitle[st.roomFocus]
  $('stageTitle').textContent = rf ? hs.name + ' · ' + rf : hs ? hs.name : tr('หมู่บ้าน Agent')
  applyPhase()
  $('stageBack').hidden = !hs
  $('stageSub').textContent = rf
    ? tr('จอมอนิเตอร์ห้อง · ข้อความล่าสุดของ agent ในห้องนี้ · คลิก agent เพื่อเปิดแชต · Esc/← กลับ')
    : hs
    ? hs.sub + tr(' · คลิกห้องเพื่อซูม · คลิก agent เพื่อดูบทสนทนา · อัปเดต ') + ago(snap.generatedAt)
    : live.length + ' agent · ' + taskSummary() + reach + tr(' รับข้อความจากเว็บได้ (● เขียว) · ') + snap.summary.needsInput + tr(' รอคุณตอบ · คลิกบ้านเพื่อเข้าไปดู')
  applyWeather()
  checkLaunches()
  renderEnergy()
  renderStats()
  renderComposerTargets()
  renderNavBadges()
  if (st.view === 'map') renderMap()
  else if (st.view === 'tasks') renderTasks()
  else if (st.view === 'pm') renderPM()
  else if (st.view === 'review') renderTaskReview()
  else renderList()
  renderTaskStrip()
  renderInspector()
  followTick()
}

// One badge for every plan, so the header stays short: the worst state and the
// highest share used. The rows are behind a click.
function renderEnergy () {
  const box = $('energy')
  const order = ['normal', 'eco', 'critical', 'reserve']
  const byProv = {}
  for (const q of st.snap.quotas) (byProv[q.providerId] = byProv[q.providerId] || []).push(q)
  const actual = st.snap.quotas.filter(q => q.confidence === 'actual')
  const worst = actual.reduce((a, q) => order.indexOf(q.mode) > order.indexOf(a) ? q.mode : a, actual.length ? 'normal' : 'guarded')
  const top = actual.length ? Math.round(Math.max(...actual.map(q => q.usedPercent))) : null
  const rows = ['anthropic', 'openai'].filter(x => byProv[x]).concat(Object.keys(byProv).filter(x => x !== 'anthropic' && x !== 'openai')).map(pid =>
    h('div', { class: 'qRow' }, h('b', null, providerName[pid] || pid),
      ...byProv[pid].map(q => h('span', { class: 'pill ' + (q.confidence === 'actual' ? q.mode : 'guarded'), title: q.source }, h('i'),
        (q.windowMinutes ? windowName(q.windowMinutes) + ' ' : '') + (q.confidence === 'actual' ? Math.round(q.usedPercent) + '%' : q.confidence === 'stale' ? tr('รีเซ็ตแล้ว') : tr('ไม่ทราบ')),
        q.confidence === 'actual' && q.resetsAt ? h('small', null, tr(' · รีเซ็ต ') + new Date(q.resetsAt).toLocaleString('th-TH', { dateStyle: 'short', timeStyle: 'short' })) : null))))
  const open = box.classList.contains('open')
  const pop = h('div', { class: 'qPop' }, ...rows, h('p', { class: 'note' }, tr('ตัวเลขคือส่วนที่ใช้ไป ป้ายแสดงค่าที่สูงสุด')))
  pop.hidden = !open
  // fixed, placed under the badge: the header clips what overflows it
  const at = box.getBoundingClientRect()
  pop.style.top = Math.round(at.bottom + 8) + 'px'
  pop.style.right = Math.max(8, Math.round(window.innerWidth - at.right)) + 'px'
  box.replaceChildren(
    h('button', { type: 'button', class: 'pill qBadge ' + worst, 'aria-expanded': String(open), title: tr('ดู quota ของแต่ละเจ้า'), onclick: e => { e.stopPropagation(); box.classList.toggle('open'); renderEnergy() } },
      h('i'), 'Quota ' + (top == null ? tr('ไม่ทราบ') : top + '%')),
    pop)
}
document.addEventListener('click', e => {
  const b = $('energy')
  if (b.classList.contains('open') && !b.contains(e.target)) { b.classList.remove('open'); if (st.snap) renderEnergy() }
})
function windowName (m) {
  if (m >= 10080) return tr('รายสัปดาห์')
  if (m >= 1440) return Math.round(m / 1440) + tr(' วัน')
  return Math.round(m / 60) + tr(' ชม.')
}

function renderStats () {
  const sm = st.snap.summary
  const items = [
    ['running', sm.running, tr('กำลังทำงาน'), ''],
    ['needsInput', sm.needsInput, tr('รอคุณตอบ'), sm.needsInput ? 'warn' : ''],
    ['rest', sm.idle + sm.completed, tr('ว่าง / เสร็จแล้ว'), ''],
    ['runsDone', sm.runsDoneToday, tr('งานเสร็จวันนี้ (agcmd)'), ''],
    ['waitingApproval', sm.waitingApproval, tr('รอตัดสินใจ'), sm.waitingApproval ? 'warn' : ''],
    ['lost', sm.lost, tr('ขาดการเชื่อมต่อ'), sm.lost ? 'bad' : '']
  ]
  $('stats').replaceChildren(...items.map(([key, n, l, c]) => h('button', { type: 'button', class: 'stat ' + c + (st.filter === key && !st.sel ? ' on' : ''), onclick: () => openFilter(key), 'aria-label': l + ' ' + n + tr(' — ดูรายการ') }, h('b', null, n), h('span', null, l))))
}

function openFilter (key) {
  st.filter = key
  st.sel = null
  st.inspKey = ''
  render()
}

const filterTitle = { running: tr('กำลังทำงาน'), needsInput: tr('รอคุณตอบ'), rest: tr('ว่าง / เสร็จแล้ว'), runsDone: tr('งาน agcmd เสร็จวันนี้'), waitingApproval: tr('รอตัดสินใจ (agcmd)'), lost: tr('ขาดการเชื่อมต่อ') }


function filterPanel (key) {
  const snap = st.snap
  const since = new Date()
  since.setHours(0, 0, 0, 0)
  let cards = []
  if (key === 'running' || key === 'needsInput' || key === 'rest') {
    const want = key === 'rest' ? ['idle', 'completed'] : [key]
    cards = snap.sessions.filter(x => x.live && want.includes(x.status)).map(sess => {
      const ex = h('div', { class: 'excerpt', 'data-sid': sess.sessionId }, key === 'needsInput' ? tr('กำลังโหลดข้อความล่าสุด…') : (sess.title || ''))
      return h('div', { class: 'card ' + (key === 'needsInput' ? 'warn' : ''), tabindex: 0, onclick: () => { st.tab = 'chat'; flyTo('session', sess.sessionId) }, onkeydown: e => { if (e.key === 'Enter') { st.tab = 'chat'; flyTo('session', sess.sessionId) } } },
        h('b', null, nameOf(sess)),
        h('small', null, (sess.repoId || '—') + ' · ' + stateText(sess) + ' · ' + ago(sess.lastEventAt) + (sess.messageVia ? tr(' · ตอบจากเว็บได้') : tr(' · ดูได้อย่างเดียว'))),
        ex)
    })
  } else {
    const runs = snap.runs.filter(r => key === 'lost' ? r.lost : key === 'waitingApproval' ? r.status === 'WAITING_DECISION' : (['DONE', 'READY'].includes(r.status) && new Date(r.updatedAt) >= since))
    cards = runs.map(run => h('div', { class: 'card', tabindex: 0, onclick: () => select('run', run.id) }, h('b', null, run.id + ' · ' + run.workspace), h('small', null, (runLabel[run.status] || run.status) + ' · ' + ago(run.updatedAt)), h('div', { class: 'excerpt' }, run.task.slice(0, 160))))
  }
  if (key === 'needsInput') loadExcerpts()
  return [
    h('button', { type: 'button', class: 'ghost', onclick: () => { st.filter = null; render() } }, tr('← ภาพรวม')),
    h('h2', null, filterTitle[key] + ' (' + cards.length + ')'),
    cards.length ? cards : h('p', { class: 'note' }, tr('ไม่มีรายการ')),
    key === 'needsInput' ? h('p', { class: 'note' }, tr('คลิกรายการเพื่อเปิดบทสนทนาแล้วพิมพ์ตอบได้ทันที')) : null
  ]
}

async function loadExcerpts () {
  for (const sess of st.snap.sessions.filter(x => x.live && x.status === 'needsInput')) {
    try {
      const res = await api('/sessions/' + encodeURIComponent(sess.sessionId) + '/messages?limit=4')
      const last = (res.messages || []).filter(m => m.role === 'assistant').pop()
      const el = document.querySelector('.excerpt[data-sid="' + sess.sessionId + '"]')
      if (el) el.textContent = last ? last.text.slice(0, 220) : tr('ไม่มีข้อความ')
    } catch (e) { /* keep placeholder */ }
  }
}

function renderComposerTargets () {
  const sel = $('askWs')
  const cur = st.target !== undefined ? st.target : sel.value
  sel.replaceChildren()
  const ready = st.snap.workspaces.filter(w => w.agentReady)
  // Sessions the web can already type to directly — a background session the
  // service relays through `claude attach`. They were left out of this list,
  // so with no tmux workspace configured the composer said "no agent ready"
  // while two agents were in fact reachable; the only way to reach them was to
  // click the character first, which is not where the composer sends you.
  const live = (st.snap.sessions || []).filter(x => x.live && x.messageVia)
  sel.append(h('option', { value: '' }, ready.length || live.length ? tr('— เลือกปลายทาง —') : tr('ไม่มี agent พร้อมรับงาน')))
  // The PM is a target like the others, and the only one that reaches no agent.
  if (st.pm) sel.append(h('option', { value: 'pm' }, tr('PM กลาง · วางแผนงาน (ยังไม่ส่งให้ agent)')))
  if (live.length) {
    const g = h('optgroup', { label: tr('agent ที่กำลังทำงาน') })
    for (const x of live) {
      g.append(h('option', { value: 'session:' + x.sessionId },
        x.displayName + ' · ' + (x.messageVia === 'relay' ? 'relay' : 'agcmd ' + x.dispatchVia)))
    }
    sel.append(g)
  }
  const usable = isUser() ? [] : st.snap.workspaces.filter(w => w.dirExists)
  if (usable.length) {
    const g = h('optgroup', { label: 'workspace (agcmd)' })
    for (const w of usable) {
      const label = w.name + (w.agentReady ? ' · ' + w.agentCommand + ' (tmux ' + w.tmuxSession + ')' : w.tmuxAlive ? tr(' · ไม่มี agent (shell)') : tr(' · ปิดอยู่'))
      g.append(h('option', { value: w.name, disabled: !w.agentReady }, label))
    }
    sel.append(g)
  }
  const pg = h('optgroup', { label: tr('ส่งตาม provider (งานใหม่)') })
  for (const [id, label] of [['auto', tr('อัตโนมัติ · agent ที่เหลือ quota มากกว่า')], ['claude', 'Claude'], ['codex', 'Codex'], ['gemini', 'Gemini']]) {
    pg.append(h('option', { value: 'provider:' + id }, label))
  }
  if (!isUser()) sel.append(pg)
  // never pick a target for the operator: only an explicit choice or the
  // selected agent's own workspace
  const stillThere = cur && ((cur === 'pm' && st.pm) || cur.startsWith('provider:') || ready.some(w => w.name === cur) || live.some(x => 'session:' + x.sessionId === cur))
  // a user has one room, so their only agent is the obvious target
  // reading someone else's room: typing is off, so nothing meant for them
  // lands in the user's own agent
  const look = isUser() && st.sel && st.sel.kind === 'session' && st.snap.sessions.find(x => x.sessionId === st.sel.id && !x.messageVia)
  $('askText').disabled = sel.disabled = $('askClip').disabled = !!look
  sel.value = look ? '' : stillThere ? cur : isUser() && live.length === 1 ? 'session:' + live[0].sessionId : ''
  updateComposerHint()
}

function composerSession () {
  if (!st.sel || st.sel.kind !== 'session' || !st.snap) return null
  const sess = st.snap.sessions.find(x => x.sessionId === st.sel.id)
  return sess && sess.live && sess.messageVia ? sess : null
}

// Placeholders are written for a desktop composer. On a phone the textarea is
// one line tall, so a 44-character hint wraps and the second line is cut off
// inside the box — which is what "the chat box overflows" looks like. Each
// hint gets a short form for narrow screens.
const hint = (long, short) => (isPhone() ? short : long)

function updateComposerHint () {
  const sess = composerSession()
  if (isPhone()) drawSheetComposer()
  if (!sess) st.suggest = []
  setTimeout(drawSuggest, 0)
  const chip = $('askTarget')
  $('askWs').hidden = !!sess
  $('askMode').hidden = isUser() || !!sess
  chip.hidden = !sess
  if (sess) {
    chip.replaceChildren(h('span', null, tr('ส่งถึง: ') + sess.displayName), h('small', null, sess.messageVia === 'relay' ? 'relay' : 'agcmd ' + sess.dispatchVia), h('button', { type: 'button', class: 'ghost', title: tr('เปลี่ยนเป็นส่งตาม workspace'), onclick: () => { back() } }, '×'))
    $('askText').placeholder = tr('พิมพ์ถึง ') + sess.displayName +
      hint(tr('… (Enter ส่ง · Shift+Enter ขึ้นบรรทัด · วาง/ลากรูปได้)'), '…')
    $('askGo').disabled = sendingIn.has($('askText'))
    return
  }
  const ws = $('askWs').value
  const picked = targetSession(ws)
  const w = ws && st.snap && st.snap.workspaces.find(x => x.name === ws)
  const input = $('askText')
  if (st.blockReason) {
    input.placeholder = hint(st.blockReason, tr('ส่งจากเว็บไม่ได้'))
  } else if (picked) {
    input.placeholder = tr('พิมพ์ถึง ') + picked.displayName +
      hint(tr('… (Enter ส่ง · Shift+Enter ขึ้นบรรทัด)'), '…')
  } else if (ws === 'pm') {
    input.placeholder = hint(tr('เล่าให้ PM ฟังว่าอยากได้อะไร… (Enter ส่ง)'), tr('บอก PM…'))
  } else if (ws.startsWith('provider:')) {
    input.placeholder = hint(tr('งานใหม่ให้ agent ที่ว่าง…'), '…')
  } else if (w) {
    input.placeholder = tr('ส่งถึง ') + w.name +
      hint(' (' + w.agentCommand + tr(' ใน tmux ') + w.tmuxSession + ')…', '…')
  } else {
    input.placeholder = hint(tr('เลือกปลายทางก่อน หรือคลิกตัวละครที่รับงานได้'), tr('เลือกปลายทางก่อน'))
  }
  // A mission is an agcmd run and needs a workspace; a session can only be
  // messaged, so hide the mode picker rather than offer a choice that fails.
  $('askMode').hidden = isUser() || !!picked || ws === 'pm'
  // A provider target and the PM are sendable too. Send used to be disabled
  // for "by provider", which left a phone — no Enter-to-send — unable to use it.
  $('askGo').disabled = sendingIn.has($('askText')) || (!w && !picked && ws !== 'pm' && !ws.startsWith('provider:'))
}

// targetSession resolves a "session:<id>" value from the target picker.
function targetSession (value) {
  if (!value || !value.startsWith('session:') || !st.snap) return null
  const id = value.slice('session:'.length)
  const x = (st.snap.sessions || []).find(y => y.sessionId === id)
  return x && x.live && x.messageVia ? x : null
}

// ---------- world: village of houses + house interiors (HTML scenes) ----------
// Scenes are fixed-size boards built from the image assets and scaled to the
// stage. Agents are persistent actors: on entering a house they are already
// at their places; they only walk when something changes while you watch.

const A = name => '/aiagent/assets/' + name + '.png'
const ns2 = 'http://www.w3.org/2000/svg'

function hash (s) {
  let x = 0
  for (let i = 0; i < s.length; i++) x = (x * 31 + s.charCodeAt(i)) >>> 0
  return x
}

function img (name, x, y, w, extra) {
  const e = document.createElement('img')
  e.src = A(name)
  e.alt = ''
  e.draggable = false
  e.className = 'sprite' + (extra && extra.cls ? ' ' + extra.cls : '')
  e.style.left = x + 'px'
  e.style.top = y + 'px'
  e.style.width = w + 'px'
  if (extra && extra.z !== undefined) e.style.zIndex = extra.z
  if (extra && extra.flip) e.style.transform = 'scaleX(-1)'
  return e
}

function box (cls, x, y, w, hgt) {
  const e = document.createElement('div')
  e.className = cls
  e.style.left = x + 'px'
  e.style.top = y + 'px'
  if (w) e.style.width = w + 'px'
  if (hgt) e.style.height = hgt + 'px'
  return e
}

// The village is one house. Sessions come and go and there is no limit on how
// many there are, so dividing five painted buildings between them never held —
// the list view is where you pick one agent out of many. Here the whole place
// is entered at once and everyone is inside, standing in the room that matches
// what they are doing.
const HOUSE = 'village'
// the lit plaza at the middle of the painting, which is what you click
const ENTRANCE = { x: 560, y: 300, w: 520, h: 340 }

const roomOfActivity = {
  codeEdit: 'code', testRun: 'test', deploy: 'deploy', command: 'ops', research: 'library', planning: 'plan', unknown: 'plan', idle: 'lounge'
}
const earColors = ['#F26D7D', '#6C8CF5', '#9B7BE0', '#F2A65A', '#3FC1C9', '#F08CC0', '#7BC96F', '#E8C547']

// Everyone live on this box, plus the sub-agents they spawned and the agcmd
// runs waiting on you.
function actorsFor () {
  const out = []
  for (const sess of st.snap.sessions) {
    if (!sess.live) continue
    const s2 = sess.status
    const waiting = s2 === 'needsInput'
    const resting = s2 === 'idle' || s2 === 'completed'
    const ci = (sess.watches || []).find(w => w.ci)
    const rev = openReviewsOf(sess.sessionId).length
    const room = waiting ? 'approval' : (resting && ci) ? 'deploy' : (resting && rev) ? 'test' : resting ? 'lounge' : (roomOfActivity[sess.activityType] || 'plan')
    out.push({
      kind: 'session',
      id: sess.sessionId,
      room,
      runtime: sess.runtimeType,
      mood: waiting ? 'ask' : (resting && ci) ? 'work' : (resting && rev) ? 'ask' : resting ? 'sleep' : 'work',
      label: waiting ? tr('รอคุณตอบ') : (resting && ci) ? tr('รอ CI: ') + ci.label : (resting && rev) ? tr('รอคุณตรวจงาน (') + rev + ')' : s2 === 'completed' ? tr('เสร็จแล้ว') : resting ? tr('ว่าง') : activityLabel[sess.activityType],
      watches: sess.watches || [],
      name: (sess.dispatchVia ? '@' + sess.dispatchVia + ' ' : '') + nameOf(sess),
      canTake: !!sess.messageVia
    })
    for (const sa of sess.subAgents || []) {
      const done = sa.status === 'done'
      out.push({
        kind: 'sub',
        sub: true,
        id: sess.sessionId + ':' + sa.id,
        parent: sess.sessionId,
        room,
        runtime: 'claudeCode',
        mood: done ? 'sleep' : 'work',
        label: done ? tr('เสร็จแล้ว') : (activityLabel[sa.activityType] || tr('กำลังช่วย')),
        name: '↳ ' + (sa.type && sa.type !== 'general-purpose' ? sa.type + ': ' : '') + sa.description,
        canTake: false
      })
    }
  }
  for (const run of st.snap.runs) {
    if (run.status === 'WAITING_DECISION') out.push({ kind: 'run', id: run.id, room: 'approval', runtime: 'claudeCode', mood: 'ask', label: tr('รอคุณตัดสินใจ'), name: run.id })
    else if (run.lost) out.push({ kind: 'run', id: run.id, room: 'ops', runtime: 'claudeCode', mood: 'lost', label: tr('ขาดการเชื่อมต่อ'), name: run.id })
  }
  return out
}

// ----- scenes -----
const VW = 1660
const VH = 940

function sceneBoard (w, hgt, cls) {
  const v = $('village')
  v.replaceChildren()
  const board = box('board ' + cls, 0, 0, w, hgt)
  v.append(board)
  st.board = { el: board, w, h: hgt }
  applyZoom()
  return board
}

// On a phone the whole village fits the width at about a quarter size, too
// small to see who is who, so it opens at 2x and the child pans across it.
function zoomLevel () { return st.zoom ?? (isPhone() ? 2 : 1) }

function applyZoom () {
  const v = $('village')
  const b = st.board
  if (!b || !b.el.isConnected) return
  const inRoom = st.house && st.roomFocus
  // On a desktop nothing scrolls under the composer, so a room must fit both
  // ways or its floor is cut off; a phone pans, so there it fills the width.
  // The heading's height varies (chips, wrapping), so the scene starts below it.
  if (window.matchMedia('(min-width: 901px)').matches) {
    const head = document.querySelector('.stageHead')
    v.style.top = head.offsetTop + head.offsetHeight + 8 + 'px'
  } else v.style.top = ''
  const fit = (inRoom && isPhone() ? v.clientWidth / b.w : Math.min(v.clientWidth / b.w, v.clientHeight / b.h)) || 1
  const zl = zoomLevel()
  const z = fit * zl
  b.el.style.transform = 'translate(' + Math.max(0, (v.clientWidth - b.w * z) / 2) + 'px,' + Math.max(0, (v.clientHeight - b.h * z) / 2) + 'px) scale(' + z + ')'
  v.classList.toggle('zoomed', zl > 1)
  if (zl > 1 && st.zoomShown !== zl) {
    st.zoomShown = zl
    v.scrollLeft = Math.max(0, (b.w * z - v.clientWidth) / 2)
    v.scrollTop = Math.max(0, (b.h * z - v.clientHeight) / 2)
  }
  $('zoomVal').textContent = Math.round(zl * 100) + '%'
}

function renderMap () {
  if (st.house && st.roomFocus) renderRoomFocus()
  else if (st.house) renderInterior()
  else renderVillage()
}

function renderVillage () {
  const list = actorsFor()
  const sig = list.map(a => a.mood + a.room).join(',') + '|' + entranceTaskPills().map(p => p && p.textContent).join('|')
  if (st.drawn === 'village' && st.villageSig === sig) return
  st.villageSig = sig
  // the scenery is drawn once; a status change only redraws the entrance
  if (st.drawn === 'village' && st.vBoard && st.vBoard.isConnected) {
    for (const el of st.vBoard.querySelectorAll('.houseHit')) el.remove()
    drawVillageEntrance(st.vBoard, list)
    return
  }
  st.drawn = 'village'
  st.actors = {}
  const b = sceneBoard(VW, VH, 'village')
  st.vBoard = b
  drawVillageScenery(b)
  drawVillageEntrance(b, list)
}

function drawVillageScenery (b) {
  let seed = 11
  // The painted village floor. It replaces the SVG island and the CSS sky —
  // both are still drawn below for the pieces that move (stars, clouds,
  // petals, fireflies, the weather tint), but the ground is art now, so the
  // island and its pastel grass are skipped when the background is present.
  const bg = document.createElement('img')
  bg.src = A2('bg-village.jpg')
  bg.alt = ''
  bg.className = 'villageBg'
  b.append(bg)
  b.classList.add('painted')
  b.append(box('weatherFx', 0, 0, VW, VH))
  const fx = box('ambient', 0, 0, VW, VH)
  for (let i = 0; i < 16; i++) {
    const p = h('i', { class: 'petal' })
    p.style.left = Math.round((i * 97) % VW) + 'px'
    p.style.animationDelay = (-(i * 1.3) % 14).toFixed(1) + 's'
    p.style.animationDuration = (10 + (i % 5) * 1.7).toFixed(1) + 's'
    fx.append(p)
  }
  for (let i = 0; i < 14; i++) {
    const f = h('i', { class: 'firefly' })
    f.style.left = (180 + (i * 113) % 1300) + 'px'
    f.style.top = (380 + (i * 61) % 420) + 'px'
    f.style.animationDelay = (-(i * 0.9)).toFixed(1) + 's'
    fx.append(f)
  }
  b.append(fx)
  const rnd = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648 }
  for (let i = 0; i < 80; i++) {
    const s = box('star', Math.round(rnd() * VW), Math.round(rnd() * 230), 0, 0)
    s.style.opacity = 0.25 + (i % 4) * 0.15
    b.append(s)
  }
  b.append(h('div', { class: 'sunmoon' }))
  // Daytime cartoon clouds over a painted night skyline read as grey smudges.
  if (!b.classList.contains('painted')) {
    b.append(img('cloud1', 40, 120, 170, { cls: 'drift' }), img('cloud2', 1440, 90, 150, { cls: 'drift slow' }), img('cloud1', 980, 40, 120, { cls: 'drift slow' }), img('cloud2', 330, 60, 110, { cls: 'drift' }))
  }
  // terraced island: shadow, two stone tiers with brick lines, grass top, plaza and paths
  const isl = document.createElementNS(ns2, 'svg')
  isl.setAttribute('viewBox', '0 0 ' + VW + ' ' + VH)
  isl.setAttribute('class', 'island')
  const top = 'M90 560 Q 70 380 300 300 L 700 215 Q 830 190 960 215 L 1360 300 Q 1600 380 1575 560 L 1560 650 Q 1520 820 1120 880 L 540 880 Q 140 820 105 650 Z'
  isl.innerHTML = '<defs>' +
    '<linearGradient id="gr2" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#9AD982"/><stop offset=".55" stop-color="#7CC56B"/><stop offset="1" stop-color="#66B05C"/></linearGradient>' +
    '<linearGradient id="st2" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#E9D6BE"/><stop offset="1" stop-color="#BFA58A"/></linearGradient>' +
    '<pattern id="brick" width="56" height="22" patternUnits="userSpaceOnUse"><path d="M0 11h56M28 0v11M0 11v11M56 11v11" stroke="#A88E74" stroke-width="2" fill="none" opacity=".55"/></pattern>' +
    '<radialGradient id="plaza" cx=".5" cy=".5" r=".5"><stop offset="0" stop-color="#FBF0E2"/><stop offset="1" stop-color="#EBD9C4"/></radialGradient></defs>' +
    '<ellipse cx="830" cy="905" rx="760" ry="40" fill="rgba(20,20,50,.35)"/>' +
    '<path transform="translate(0,58)" d="' + top + '" fill="url(#st2)"/><path transform="translate(0,58)" d="' + top + '" fill="url(#brick)"/>' +
    '<path transform="translate(0,30)" d="' + top + '" fill="#D9C2A6"/><path transform="translate(0,30)" d="' + top + '" fill="url(#brick)" opacity=".7"/>' +
    '<path d="' + top + '" fill="url(#gr2)"/>' +
    '<path d="' + top + '" fill="none" stroke="#F3E6D2" stroke-width="10" opacity=".9"/>' +
    '<g fill="#F1E3D1" stroke="#E2CDB4" stroke-width="3">' +
    '<path d="M330 470 Q 520 560 640 610 L 660 640 Q 520 600 300 500 Z"/>' +
    '<path d="M1330 470 Q 1140 560 1020 610 L 1000 640 Q 1140 600 1360 500 Z"/>' +
    '<path d="M300 690 Q 480 690 600 670 L 610 700 Q 470 720 300 720 Z"/>' +
    '<path d="M1360 690 Q 1180 690 1060 670 L 1050 700 Q 1190 720 1360 720 Z"/>' +
    '<path d="M790 480 L 870 480 L 880 540 L 780 540 Z"/>' +
    '<path d="M780 760 L 880 760 L 930 900 L 730 900 Z"/></g>' +
    '<ellipse cx="830" cy="650" rx="250" ry="118" fill="url(#plaza)" stroke="#E2CDB4" stroke-width="4"/>'
  // The island is the pastel ground. With a painted floor it is redundant,
  // and drawing it over the art would put a green hill on a neon plaza.
  if (!b.classList.contains('painted')) b.append(isl)
  // No prop sprites are placed on top. The map is a finished painting — its own
  // trees, lamps, railings, water and fountain — so the pastel set that used to
  // stand here was a second copy of everything, in the wrong style.
}

// ----- A1 weather follows the plan usage -----
function applyWeather () {
  const used = (st.snap.quotas || []).filter(q => q.confidence === 'actual' && q.usedPercent != null).map(q => q.usedPercent)
  const worst = used.length ? Math.max(...used) : -1
  const w = worst < 0 ? 'clear' : worst >= 85 ? 'storm' : worst >= 50 ? 'cloudy' : 'clear'
  document.body.dataset.weather = w
  const chip = $('phaseChip')
  if (chip && worst >= 0) chip.title = tr('อากาศตาม quota: ใช้ไปสูงสุด ') + Math.round(worst) + '% (' + ({ clear: tr('แดดใส'), cloudy: tr('เมฆมาก'), storm: tr('พายุ') })[w] + ')'
  if (chip && w !== 'clear' && !chip.textContent.includes(w === 'storm' ? '⛈' : '☁')) chip.textContent += w === 'storm' ? ' · ⛈' : ' · ☁'
}

// ----- A2 the Deploy rocket reacts to finished CI -----
function checkLaunches () {
  st.launched = st.launched || {}
  const fresh = []
  for (const sess of st.snap.sessions) {
    const ci = sess.lastCI
    if (!ci || ci.ok == null) continue
    const key = sess.sessionId + '|' + ci.eventAt
    if (st.launched[key]) continue
    st.launched[key] = true
    // only events from the last 2 minutes animate (not old ones on page load)
    if (st.launchReady && Date.now() - new Date(ci.eventAt).getTime() < 120000) fresh.push(Object.assign({ who: sess.displayName }, ci))
  }
  st.launchReady = true
  for (const ci of fresh) launch(ci)
}
function launch (ci) {
  toast((ci.ok ? tr('🚀 CI ผ่าน: ') : tr('💨 CI ไม่ผ่าน: ')) + ci.label + ' · ' + ci.who, !ci.ok)
  for (const r of document.querySelectorAll('.rocketItem')) {
    r.classList.remove('launch', 'fizzle')
    void r.offsetWidth
    r.classList.add(ci.ok ? 'launch' : 'fizzle')
    setTimeout(() => r.classList.remove('launch', 'fizzle'), 4200)
  }
  const dep = document.querySelector('.houseHit')
  if (dep && st.board) {
    const x = parseFloat(dep.style.left) + 140
    const y = parseFloat(dep.style.top) + 20
    const rk = img('rocket', x, y, 70, { cls: ci.ok ? 'villageRocket' : 'villageRocket fizzle' })
    st.board.el.append(rk)
    setTimeout(() => rk.remove(), 4300)
  }
}

function drawVillageEntrance (b, list) {
  const busy = list.filter(a => a.mood === 'work').length
  const ask = list.filter(a => a.mood === 'ask').length
  const g = box('houseHit', ENTRANCE.x, ENTRANCE.y, ENTRANCE.w, ENTRANCE.h)
  g.dataset.key = HOUSE
  g.style.zIndex = ENTRANCE.y + 200
  g.tabIndex = 0
  g.setAttribute('role', 'button')
  g.setAttribute('aria-label', tr('เข้าไปในบ้าน Agent') + ' — ' + list.length + ' agent')
  g.append(h('i', { class: 'houseLit' + (busy ? ' busy' : '') }))
  g.append(h('div', { class: 'hcard green' },
    h('div', { class: 'hicon' }, iconSvg('lounge')),
    h('div', { class: 'htext' },
      h('b', null, h('i', { class: 'dot' }), tr('บ้าน Agent') + ' · ' + list.length),
      h('span', null, tr('คลิกเพื่อเข้าไปดูทุกห้อง')),
      h('div', { class: 'hrow' },
        ask ? h('em', { class: 'pill ask' }, '⏸ ' + ask + tr(' รอคุณตอบ')) : '',
        busy ? h('em', { class: 'pill work' }, '✦ ' + busy + tr(' กำลังทำงาน')) : '',
        ...entranceTaskPills(),
        !ask && !busy ? h('em', { class: 'pill idle' }, list.length ? tr('ว่างทั้งหมด') : tr('ไม่มี agent')) : '')),
    h('span', { class: 'pin' })))
  const enter = () => {
    stopFollow()
    st.house = HOUSE; st.sel = null; st.inspKey = ''; render()
  }
  g.addEventListener('click', enter)
  g.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); enter() } })
  b.append(g)
}

// One glyph per room key, plus the three house markers on the map. These are
// paths and not PNGs on purpose (docs/REDESIGN_SPEC.md §2): the colour has to
// come from the room through currentColor, which a drawn icon cannot do.
//
// Each is a single path, stroke only, inside the 20x20 safe area of the 24x24
// box so it still reads at the 20px the workflow stepper draws it at.
const iconPaths = {
  plan: 'M8 4h8 M9 3h6v3H9z M7 5H5v16h14V5h-2 M8 11l1.5 1.5L12 10 M14 11h3 M8 16l1.5 1.5L12 15 M14 16h3',
  library: 'M12 5v15 M12 6c-2.5-2-5.2-2.1-9-1v14c3.7-1.2 6.5-.9 9 1 M12 6c2.5-2 5.2-2.1 9-1v14c-3.7-1.2-6.5-.9-9 1',
  code: 'M9 7L4 12l5 5 M15 7l5 5-5 5',
  test: 'M9 3h6 M11 3v7l-6.9 9.7A1.5 1.5 0 0 0 5.3 22h13.4a1.5 1.5 0 0 0 1.2-2.3L13 10V3 M7 17h10',
  ops: 'M10 2h4l.6 2.1 1.7.7 1.9-1.1 2.8 2.8-1.1 1.9.7 1.7L23 10v4l-2.1.6-.7 1.7 1.1 1.9-2.8 2.8-1.9-1.1-1.7.7L14 23h-4l-.6-2.1-1.7-.7-1.9 1.1L3 18.5l1.1-1.9-.7-1.7L1 14v-4l2.1-.6.7-1.7L2.7 5.8 5.5 3l1.9 1.1 1.7-.7L10 2z M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z',
  deploy: 'M14 3c3 1 6 4 7 7-1 4-4 7-8 9l-5-5C10 10 11 6 14 3z M8 14l-4 1-2 5 5-2 1-4 M12 19l-1 3-5 0 1-4 M14 9a1.4 1.4 0 1 0 0 2.8A1.4 1.4 0 0 0 14 9z',
  approval: 'M12 2L21 6v6c0 5-3.2 8.2-9 10-5.8-1.8-9-5-9-10V6z M7.5 12l3 3 6-6',
  lounge: 'M4 8h13v7a5 5 0 0 1-5 5H9a5 5 0 0 1-5-5z M17 9h2a3 3 0 0 1 0 6h-2 M3 21h17',
  // house markers: the map keeps its own three, which are not room glyphs
  term: 'M4 7l5 5-5 5M12 17h8',
  layers: 'M12 3l9 5-9 5-9-5zM3 13l9 5 9-5',
  rocket: 'M12 3c3 2 5 6 4 10l-4 4-4-4c-1-4 1-8 4-10zM9 17l-2 4M15 17l2 4M12 9.5v.1'
}

// The gear is drawn teeth-to-edge, 1 to 23, so beside the other seven it
// renders a notch larger and crowds them in the stepper. Fit it back into the
// 20x20 safe area about the centre: 12 + (v - 12) * 10/11.
const iconFit = { ops: 'translate(1.0909 1.0909) scale(0.90909)' }

function iconSvg (kind) {
  const v = document.createElementNS(ns2, 'svg')
  v.setAttribute('viewBox', '0 0 24 24')
  const d = iconPaths[kind] || 'M4 12h16'
  const t = iconFit[kind] ? ' transform="' + iconFit[kind] + '"' : ''
  v.innerHTML = '<path d="' + d + '"' + t + ' fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>'
  return v
}

// ----- interior (room scenes from the art) -----
// The house is one workflow: 1 plan → 2 research → 3 code → 4 test on the
// top row, then 5 infra → 6 deploy → 7 waiting → 8 lounge below. One hall
// runs between the rows; agents come and go through the side door on its
// left. Wide (≈1.85:1) so it fills the stage with or without the panel.
const CW = 440
const CH = 275
const IW = 2060
const IH = 1130
const hallA = 586
const sideX = 40
const rooms2 = {
  plan: { col: 0, row: 0, step: 1 },
  library: { col: 1, row: 0, step: 2 },
  code: { col: 2, row: 0, step: 3 },
  test: { col: 3, row: 0, step: 4 },
  ops: { col: 0, row: 1, step: 5 },
  deploy: { col: 1, row: 1, step: 6 },
  approval: { col: 2, row: 1, step: 7 },
  lounge: { col: 3, row: 1, step: 8 }
}
const roomDesc = {
  plan: tr('กำหนดเป้าหมาย วางแผนงาน และแบ่งงาน'),
  library: tr('ค้นคว้า รวบรวมข้อมูล และวิเคราะห์'),
  code: tr('พัฒนาและปรับปรุงตามแผน'),
  test: tr('ทดสอบการทำงาน ตรวจสอบคุณภาพ'),
  ops: tr('ดูแลระบบ สภาพแวดล้อม และทรัพยากร'),
  deploy: tr('เตรียม Build และ Deploy ขึ้นใช้งาน'),
  approval: tr('รอคำตอบหรือการยืนยันจากคุณ'),
  lounge: tr('พักและเตรียมพร้อมสำหรับงานถัดไป')
}
const roomShort = { plan: tr('วางแผน'), library: tr('วิจัย'), code: tr('พัฒนา'), test: tr('ทดสอบ'), ops: tr('ระบบ'), deploy: 'Deploy', approval: tr('รอคุณตอบ'), lounge: tr('พัก') }
for (const k in rooms2) {
  const r = rooms2[k]
  r.x = 92 + r.col * (CW + 52)
  r.y = r.row ? 660 : 236
  r.top = r.row > 0
  r.hall = hallA
  r.door = r.x + CW * 0.84
}
const frontDoor = { x: sideX, y: hallA }

function roomSeats () {
  const out = []
  for (const i of [0, 2, 4, 1, 3]) out.push([90 + i * 140, 410], [160 + i * 140, 472])
  return out
}

function seatsOf (key) {
  const r = rooms2[key]
  const k = CW / RW
  return roomSeats().map(([x, y]) => [r.x + x * k, r.y + y * k])
}

// ----- composed rooms on painted empty rooms -----
// bg: empty room art (800×500 space). screen: [frame, x, y, w, title]
// the message area sits inside the frame's glass (screenGlass fractions).
const RW = 800
const RH = 500
const screenGlass = {
  'scr-lamp': [0.07, 0.25, 0.93, 0.88], 'scr-blue': [0.09, 0.2, 0.91, 0.84], 'scr-bunny': [0.07, 0.33, 0.93, 0.88],
  'scr-white': [0.06, 0.1, 0.94, 0.84], 'scr-robot': [0.1, 0.25, 0.9, 0.85], 'scr-tablet': [0.1, 0.12, 0.9, 0.75]
}
const screenRatio = { 'scr-lamp': 0.57, 'scr-blue': 0.57, 'scr-bunny': 0.77, 'scr-white': 0.63, 'scr-robot': 0.55, 'scr-tablet': 0.77 }
const roomDefs = {
  code: { th: tr('ห้องพัฒนา'), en: 'Development', bg: 'bg-code', screen: ['scr-robot', 348, 92, 340, '</> Dev Monitor'], clock: [70, 150, 38],
    items: [['plant-big', 6, 330, 74], ['desk-blue2', 64, 296, 180], ['desk-pink2', 310, 296, 180], ['desk-white2', 556, 296, 180], ['cactus', 742, 404, 48]] },
  test: { th: tr('ห้องทดสอบ'), en: 'Testing', bg: 'bg-test', screen: ['scr-blue', 230, 70, 340, '▶ Test Runner'],
    items: [['filing-blue', 600, 214, 70], ['desk-white2', 70, 290, 180], ['desk-blue2', 300, 290, 180], ['printer2', 640, 310, 116], ['hydrangea', 14, 404, 64]] },
  library: { th: tr('ห้องวิจัย'), en: 'Research', bg: 'bg-library', screen: ['scr-white', 180, 78, 320, '📖 Research Notes'],
    items: [['shelf-big', 70, 176, 210], ['shelf-tall', 566, 190, 96], ['globe', 680, 296, 56], ['table-meet', 284, 292, 232], ['plant-stand', 30, 380, 66], ['books2', 600, 420, 70]] },
  plan: { th: tr('ห้องวางแผน'), en: 'Planning', bg: 'bg-plan', screen: ['scr-white', 368, 45, 340, '◷ Plan Board'], clock: [745, 150, 40],
    items: [['desk-boss2', 70, 270, 240], ['table-meet', 450, 296, 226], ['plant-big', 8, 362, 70], ['calendar', 720, 420, 56]] },
  approval: { th: tr('รอคุณตอบ'), en: 'Waiting for Reply', bg: 'bg-approval', screen: ['scr-bunny', 250, 40, 300, tr('💬 รอคุณตอบ')],
    items: [['reception2', 34, 312, 260], ['sofa-yellow2', 480, 330, 270], ['water', 734, 268, 58], ['flowers-pot', 330, 420, 52], ['pillow-bunny', 520, 352, 52]] },
  ops: { th: tr('ห้องระบบ'), en: 'Infrastructure', bg: 'bg-ops', screen: ['scr-robot', 213, 62, 340, '◉ System Monitor'],
    items: [['servers2', 22, 200, 196], ['filing-blue', 700, 262, 66], ['desk-white2', 300, 316, 180], ['printer2', 556, 362, 104], ['plant-big', 722, 380, 70]] },
  deploy: { th: tr('ห้อง Deploy'), en: 'Deploy', bg: 'bg-deploy', screen: ['scr-lamp', 367, 70, 350, '🚀 Deployment Board'],
    items: [['desk-pink2', 24, 324, 176], ['desk-pink2', 600, 324, 176], ['rug-pink2', 240, 404, 320], ['rocket', 352, 352, 96], ['bean-pink', 266, 404, 84], ['bean-yellow', 452, 404, 84], ['box-pallet', 220, 320, 74], ['handtruck', 528, 316, 54]] },
  lounge: { th: tr('ห้องพัก'), en: 'Lounge', bg: 'bg-lounge', screen: ['scr-tablet', 255, 40, 290, '☕ TV'], clock: [736, 214, 34],
    items: [['rug-cloud', 196, 418, 250], ['sofa-pink2', 52, 318, 276], ['armchair2', 352, 330, 112], ['hammock2', 494, 318, 236], ['bean-purple', 290, 424, 76], ['water', 742, 296, 52], ['plant-stand', 8, 392, 58]] },
  entrance: { th: tr('ทางเข้า'), en: 'Entrance', garden: true,
    items: [['signboard', 40, 160, 120], ['sakura2', 280, 70, 240], ['bench2', 560, 230, 190], ['flowerbox3', 40, 380, 150], ['lamppost', 720, 210, 46], ['bush2b', 560, 400, 160]] }
}

function composeRoom (host, key, scale, withScreen) {
  const R = roomDefs[key]
  const inner = box('roomInner', 0, 0, RW, RH)
  inner.style.transform = 'scale(' + scale + ')'
  if (R.bg) {
    const bg = document.createElement('img')
    bg.src = A2(R.bg + '.jpg')
    bg.alt = ''
    bg.className = 'roomBg'
    inner.append(bg)
    // the window sky follows the time of day (mask = sky pixels of the art)
    const sky = box('skyLayer', 0, 0, RW, RH)
    const m = 'url(' + A('sky-' + key) + ')'
    sky.style.maskImage = m
    sky.style.webkitMaskImage = m
    inner.append(sky)
  } else {
    inner.append(box('floor fl-grass', 0, 0, RW, RH))
  }
  let screen = null
  if (R.screen) {
    const [frame, sx, sy, sw, title] = R.screen
    const sh = sw * screenRatio[frame]
    const wrap = box('screenWrap', sx, sy, sw, sh)
    wrap.append(img(frame, 0, 0, sw, { cls: 'scrFrame' }))
    const g = screenGlass[frame]
    screen = box('wallScreen', g[0] * sw, g[1] * sh, (g[2] - g[0]) * sw, (g[3] - g[1]) * sh)
    screen.append(h('div', { class: 'scrHead' }, h('i'), title), h('div', { class: 'scrBody' }, h('b'), h('b'), h('b')))
    wrap.append(screen)
    inner.append(wrap)
  }
  if (R.clock) inner.append(clockEl(R.clock[0], R.clock[1], R.clock[2]))
  for (const [n, x, y, w] of R.items) inner.append(img(n, x, y, w, n === 'rocket' ? { cls: 'rocketItem' } : undefined))
  if (npcs[key]) inner.append(npcEl(key, withScreen))
  host.append(inner)
  const step = rooms2[key] && rooms2[key].step
  host.append(h('div', { class: 'rlabel2' }, step ? h('em', { class: 'step' }, String(step)) : null,
    iconPaths[key] ? h('span', { class: 'rlIcon' }, iconSvg(key)) : null,
    h('b', null, R.th), h('span', null, R.en)))
  return withScreen ? screen : null
}

// NPC guides: one resident per room who explains what the room is for
const npcs = {
  plan: ['cx-kid6', 742, tr('สวัสดี! ที่นี่ห้องวางแผน 📋'), tr('agent ที่กำลังวางแผน แตกงานเป็นขั้น หรือส่งงานให้ sub-agent จะมาอยู่ที่นี่')],
  library: ['cx-kid1', 560, tr('ห้องวิจัยค่ะ 🔎'), tr('agent ที่กำลังอ่านไฟล์ ค้นโค้ด หรือค้นข้อมูลจากเว็บ (Read · Grep · WebSearch) จะมานั่งค้นคว้าที่นี่')],
  code: ['cx-kid0', 754, tr('ห้องพัฒนา </>'), tr('agent ที่กำลังแก้ไฟล์โค้ดจริง (Edit · Write) จะมานั่งโต๊ะคอมที่นี่')],
  test: ['cx-kid4', 556, tr('ห้องทดสอบ 🧪'), tr('agent ที่กำลังรันเทส เช่น go test, bun test, playwright จะมาที่นี่ ผ่านแล้วค่อยไปห้อง Deploy')],
  ops: ['cx-kid5', 650, tr('ห้องระบบ ⚙️'), tr('agent ที่กำลังรันคำสั่งทั่วไปใน shell เช่น ตรวจเครื่อง ดู log จัดการไฟล์ จะมาอยู่ที่นี่')],
  deploy: ['cx-fox', 560, tr('ห้อง Deploy 🚀'), tr('agent ที่กำลัง build / deploy หรือรอ Jenkins · CI อยู่ จะมานั่งที่นี่ จอจะโชว์งาน CI และสถานะล่าสุด')],
  approval: ['cx-kid7', 440, tr('ห้องรอคุณตอบ 💬'), tr('agent ที่ถามคำถามหรือรอให้คุณยืนยัน จะมารอที่นี่ — คลิก agent แล้วตอบได้จากช่องด้านล่าง')],
  lounge: ['cx-panda', 470, tr('ห้องพัก ☕'), tr('agent ที่ทำงานเสร็จแล้วหรือว่างรอคำสั่งใหม่ จะมาพักที่นี่')]
}
// The bubble is kept inside the room: a guide near a wall would lose its text.
const NPC_SAY_W = 300
function npcEl (key, big) {
  const [ch, x, title, text] = npcs[key]
  const n = box('npc', x - 40, 360, 80, 140)
  n.append(img(ch, 0, 0, 80, { cls: 'npcImg' }))
  if (!big) n.title = title + ' — ' + text
  else {
    n.setAttribute('aria-label', title + ' — ' + text)
    const say = h('div', { class: 'npcSay' }, h('b', null, title), h('p', null, text))
    say.style.width = NPC_SAY_W + 'px'
    say.style.left = Math.max(10, Math.min(RW - NPC_SAY_W - 10, x - NPC_SAY_W / 2)) - (x - 40) + 'px'
    n.append(say)
    n.addEventListener('click', e => { e.stopPropagation(); n.classList.toggle('talk') })
    setTimeout(() => n.classList.add('talk'), 400)
    setTimeout(() => n.classList.remove('talk'), 6000)
  }
  return n
}

function A2 (file) { return '/aiagent/assets/' + file }

function drawInterior () {
  const b = sceneBoard(IW, IH, 'interior')
  // house: roof, body, workflow banner
  b.append(box('houseRoof', 0, -40, IW, 240), box('houseBody', 30, 168, IW - 60, IH - 188))
  for (const [n, x, y, w] of [['sakura2', -60, 760, 190], ['tree1', IW - 140, 760, 190], ['bush2', -30, 1010, 120], ['bush3', IW - 110, 1020, 120]]) b.append(img(n, x, y, w, { cls: 'houseDeco' }))
  const banner = box('wfBanner', IW / 2 - 330, 52, 660, 128)
  banner.append(h('b', null, 'AI Agent Workflow'), h('span', null, tr('ลำดับการทำงานของ Agent ตั้งแต่การวางแผนจนถึงพัก')), img('cx-bunny', -36, 6, 70, { cls: 'wfBunny' }))
  b.append(banner)
  b.append(box('hall', 70, hallA - 30, IW - 140, 60))
  b.append(box('sideDoor', 26, hallA - 46, 48, 92))
  b.append(box('flowLine', rooms2.ops.door, hallA - 3, rooms2.test.door - rooms2.ops.door, 6))
  b.append(img('arr-turn', rooms2.test.door - 30, hallA - 46, 60, { cls: 'flowTurn' }))
  for (const key in rooms2) {
    const r = rooms2[key]
    const card = box('roomCard', r.x, r.y, CW, CH)
    composeRoom(card, key, CW / RW, false)
    const door = box('doorGap ' + (r.top ? 'top' : 'bottom'), CW * 0.84 - 32, r.top ? -9 : CH - 7, 64, 16)
    door.append(h('i', { class: 'leaf l' }), h('i', { class: 'leaf r' }))
    r.doorEl = door
    card.append(door)
    card.append(h('div', { class: 'roomCaption' }, roomDesc[key]))
    r.cardEl = card
    r.sigEl = h('button', { type: 'button', class: 'roomSig', hidden: true, title: tr('เปิดห้องดูรายละเอียด'), onclick: e => { e.stopPropagation(); openRoom(key) } })
    card.append(r.sigEl)
    // the tasks at this step of the flow; fillRoomTasks keeps it current
    r.taskEl = h('div', { class: 'roomTasks' })
    card.append(r.taskEl)
    card.classList.add('zoomable')
    card.title = tr('คลิกเพื่อซูมเข้าห้อง · จอมอนิเตอร์ข้อความ')
    card.addEventListener('click', e => { if (!e.target.closest('.actor')) openRoom(key) })
    b.append(card)
    const plq = img('plq-' + key, r.x + 14, r.y - 40, 214, { cls: 'plaque' })
    const stepEl = box('plqStep', r.x - 4, r.y - 30, 34, 34)
    stepEl.textContent = String(r.step)
    b.append(plq, stepEl)
    // A drawn chevron rather than arr-pill.png: that asset is a pale lilac
    // pill from the pastel set and was the last thing in the house still
    // wearing it. Drawn here, it takes the room's own neon.
    if (r.col < 3) b.append(box('flowArrow2 cssArrow', r.x + CW + 6, r.y + CH / 2 - 20, 40, 40))
  }
  // overview strip: click a step to open that room
  const strip = box('flowStrip', 92, IH - 110, IW - 184, 70)
  strip.append(h('b', { class: 'fsTitle' }, tr('ภาพรวมการทำงาน')))
  for (const key of Object.keys(rooms2).sort((x, y) => rooms2[x].step - rooms2[y].step)) {
    strip.append(h('button', { type: 'button', class: 'fsStep s' + rooms2[key].step, onclick: () => openRoom(key) }, h('i', null, String(rooms2[key].step)), h('span', { class: 'fsIcon' }, iconSvg(key)), roomShort[key]))
    if (rooms2[key].step < 8) strip.append(h('span', { class: 'fsArr' }, '→'))
  }
  b.append(strip)
  const layer = box('actors', 0, 0, IW, IH)
  b.append(layer)
  return layer
}

function doorPoint (key, inside) {
  const r = rooms2[key]
  return r.top ? [r.door, r.y + (inside ? 30 : -6)] : [r.door, r.y + CH + (inside ? -30 : 6)]
}

function routeTo (act, roomKey, target) {
  const pts2 = []
  if (act.room && rooms2[act.room]) pts2.push(doorPoint(act.room, true), doorPoint(act.room, false), [rooms2[act.room].door, hallA])
  else pts2.push([frontDoor.x + 30, hallA])
  if (roomKey === 'out') { pts2.push([frontDoor.x, hallA]); return pts2 }
  const to = rooms2[roomKey]
  pts2.push([to.door, hallA], doorPoint(roomKey, false), doorPoint(roomKey, true), target)
  return pts2
}

const fxFor = { ask: 'fx-bang', sleep: 'fx-zz', lost: 'fx-q', think: 'fx-bulb', done: 'fx-check' }

const chars = ['dev', 'tester', 'infra', 'deploy', 'research', 'designer']
const stillChars = [...Array(9).keys()].map(i => 'cx-kid' + i)
  .concat(['bunny', 'cat', 'husky', 'panda', 'penguin', 'fox', 'bear', 'deer', 'elephant'].map(x => 'cx-' + x))
  .concat([...Array(24).keys()].map(i => 'cx-bot' + (i + 1)))
const charPool = chars.concat(stillChars)
const animSpec = { walkR: [6, 0.62], walkL: [6, 0.62], work: [4, 0.9], think: [4, 1.6], rest: [4, 2.4], cheer: [4, 1.2] }

// A character the grown-up chose for one agent in this browser; otherwise the
// one its id hashes to.
// A look saved on the server is the same on every browser and for every role;
// one picked before that existed stays in this browser.
function sharedLook (id) {
  const s = st.snap && st.snap.sessions && st.snap.sessions.find(x => x.sessionId === id)
  return s ? s.look || '' : ''
}
const charKey = id => 'aiagentChar:' + id
function charFor (id) {
  const sh = sharedLook(id)
  if (charPool.includes(sh)) return sh
  let pick = ''
  try { pick = localStorage.getItem(charKey(id)) || '' } catch (e) { pick = '' }
  if (charPool.includes(pick)) {
    liftLook(id, pick)
    return pick
  }
  return chars[hash(id) % chars.length]
}

// A pick that lives only in this browser (made before looks were saved on the
// server) is saved there once, so the phone app and other browsers show it too.
const lifted = new Set()
function liftLook (id, c) {
  if (lifted.has(id)) return
  const own = st.snap && st.snap.sessions && st.snap.sessions.find(x => x.sessionId === id)
  if (!own || own.look || own.readOnly || (own.protected && isUser())) return
  lifted.add(id)
  own.look = c
  api('/sessions/' + encodeURIComponent(id) + '/look', { method: 'POST', body: JSON.stringify({ look: c }) })
    .catch(() => { own.look = '' })
}

function repaintActor (act) {
  if (!act || !act.spr) return
  act.char = charFor(act.id)
  act.anim2 = ''
  setAnim(act, act.walking ? act.walkDir || 'walkR' : act.mood || 'rest')
}

function chooseChar (id, c) {
  const own = st.snap && st.snap.sessions && st.snap.sessions.find(x => x.sessionId === id)
  if (own && !own.readOnly && !(own.protected && isUser())) {
    own.look = c
    api('/sessions/' + encodeURIComponent(id) + '/look', { method: 'POST', body: JSON.stringify({ look: c }) }).catch(e => toast(e.message))
  }
  try {
    if (c) localStorage.setItem(charKey(id), c)
    else localStorage.removeItem(charKey(id))
  } catch (e) { /* private mode: the pick lasts this visit */ }
  repaintActor(st.actors && st.actors[id])
  repaintActor(st.focus && st.focus.actors[id])
  render()
}

function moodAnim (a) {
  if (a.mood === 'work') return a.label === activityLabel.planning || a.label === activityLabel.research ? 'think' : 'work'
  if (a.mood === 'ask') return 'think'
  if (a.label === tr('เสร็จแล้ว')) return 'cheer'
  return 'rest'
}

function setAnim (act, anim) {
  if (!act.spr || act.anim2 === anim) return
  act.anim2 = anim
  if (act.char.startsWith('cx-')) {
    // still art: the pose is a CSS motion (hop, sway, doze) on one image
    act.spr.style.backgroundImage = 'url(' + A(act.char) + ')'
    act.spr.className = 'spr still a-' + anim
    return
  }
  const [n, d] = animSpec[anim]
  act.spr.style.backgroundImage = 'url(' + A('ch-' + act.char + '-' + anim) + ')'
  act.spr.style.setProperty('--n', n)
  act.spr.style.setProperty('--d', d + 's')
  act.spr.style.backgroundSize = (n * 100) + '% 100%'
  // restart the strip so a new pose starts on its first frame
  act.spr.classList.remove('play')
  void act.spr.offsetWidth
  act.spr.classList.add('play')
}

function actorNode (a) {
  const d = document.createElement('div')
  d.tabIndex = 0
  d.setAttribute('role', 'button')
  return d
}

function fillActor (act, a) {
  const d = act.el
  const sel = st.sel && st.sel.id === a.id
  d.className = 'actor m-' + a.mood + (sel ? ' sel' : '') + (act.walking ? ' walking' : '') + (a.sub ? ' sub' : '') + (a.runtime === 'codexCli' ? ' rt-codex' : ' rt-claude')
  d.setAttribute('aria-label', a.name + ' — ' + a.label)
  d.title = a.name + ' — ' + a.label
  d.replaceChildren()
  const fx = a.mood === 'ask' ? fxFor.ask : a.mood === 'lost' ? fxFor.lost : a.label === tr('เสร็จแล้ว') ? fxFor.done : ''
  if (sel || a.mood === 'lost') d.append(h('div', { class: 'say ' + a.mood }, a.label))
  else if (fx) d.append(img(fx, 0, 0, 26, { cls: 'fx' }))
  act.spr = h('div', { class: 'spr play' })
  act.anim2 = ''
  d.append(act.spr)
  d.append(h('span', { class: 'rtBadge', title: runtimeName[a.runtime] || '', 'aria-hidden': 'true' }, a.runtime === 'codexCli' ? '>_' : a.runtime === 'agyCli' ? '▲' : '✻'))
  const nm = a.name.length > 12 ? a.name.slice(0, 11) + '…' : a.name
  d.append(h('div', { class: 'tag' + (a.canTake ? ' reach' : '') + (a.runtime === 'codexCli' ? ' codex' : '') }, a.canTake ? h('i') : null, nm))
  act.mood = moodAnim(a)
  setAnim(act, act.walking ? act.walkDir || 'walkR' : act.mood)
}

// ----- room focus: one room large + a live message monitor -----
const roomTitle = {
  code: tr('ห้องพัฒนา'), test: tr('ห้องทดสอบ'), library: tr('ห้องวิจัย'), plan: tr('ห้องบริหาร · วางแผน'), approval: tr('รอคุณตอบ'),
  ops: tr('ห้องระบบ'), deploy: tr('ห้อง Deploy'), lounge: tr('ห้องพัก'), entrance: tr('ทางเข้า')
}
const FW = 1520
const FH = 950

function flyTo (kind, id) {
  const a = actorsFor().find(x => x.id === id)
  if (st.view !== 'map') setView('map')
  st.house = HOUSE
  st.roomFocus = null
  st.drawn = ''
  render()
  if (!a || !rooms2[a.room]) return select(kind, id)
  const r = rooms2[a.room]
  const b = st.board
  const v = $('village')
  requestAnimationFrame(() => {
    const z = Math.min(v.clientWidth / (CW * 1.5), v.clientHeight / (CH * 1.5))
    b.el.style.transition = 'transform .8s cubic-bezier(.2,.8,.2,1)'
    b.el.style.transform = 'translate(' + (v.clientWidth / 2 - (r.x + CW / 2) * z) + 'px,' + (v.clientHeight / 2 - (r.y + CH / 2) * z) + 'px) scale(' + z + ')'
    setTimeout(() => {
      b.el.style.transition = ''
      st.roomFocus = a.room
      st.drawn = ''
      select(kind, id)
    }, 850)
  })
}

function openRoom (key, byFollow) {
  if (!byFollow && st.follow && followActor() && followActor().room !== key) stopFollow()
  st.roomFocus = key
  st.drawn = ''
  render()
}


// ----- living rooms: points of interest, roaming, drag & drop, real clock, day phases -----
// Spots are in room coordinates (feet position). pose: work | sit | lie | stand
const roomPois = {
  code: [[146, 455, 'work'], [392, 455, 'work'], [638, 455, 'work'], [270, 486, 'stand'], [514, 488, 'stand'], [760, 486, 'stand']],
  test: [[148, 448, 'work'], [378, 448, 'work'], [560, 470, 'stand'], [260, 488, 'stand'], [60, 488, 'stand'], [720, 488, 'stand']],
  library: [[318, 448, 'sit'], [484, 448, 'sit'], [400, 490, 'stand'], [150, 470, 'stand'], [660, 480, 'stand']],
  plan: [[190, 410, 'work'], [478, 450, 'sit'], [650, 450, 'sit'], [340, 488, 'stand'], [748, 486, 'stand']],
  approval: [[164, 400, 'work'], [560, 420, 'sit'], [650, 420, 'sit'], [710, 420, 'sit'], [380, 484, 'stand'], [80, 486, 'stand'], [300, 470, 'stand']],
  ops: [[378, 476, 'work'], [120, 420, 'stand'], [600, 486, 'stand'], [240, 486, 'stand'], [740, 486, 'stand']],
  deploy: [[100, 476, 'work'], [676, 476, 'work'], [308, 470, 'sit'], [494, 470, 'sit'], [400, 492, 'stand'], [200, 488, 'stand']],
  lounge: [[132, 400, 'sit'], [210, 400, 'sit'], [290, 400, 'sit'], [408, 412, 'sit'], [612, 392, 'lie'], [328, 482, 'sit'], [470, 488, 'stand'], [60, 488, 'stand']],
  entrance: [[200, 470, 'stand'], [400, 480, 'stand'], [650, 360, 'sit'], [720, 470, 'stand']]
}
const doorSpot = [400, 520]

function focusPoiXY (key, i) {
  const p = roomPois[key][i]
  return [p[0] * st.focus.FS, p[1] * st.focus.FS]
}

function poseFor (a) {
  if (a.mood === 'work') return ['work', 'stand']
  if (a.mood === 'ask') return ['stand', 'sit']
  return ['sit', 'lie', 'stand']
}

function pickPoi (act, a) {
  const pois = roomPois[st.focus.key]
  const taken = new Set(Object.values(st.focus.actors).filter(o => o !== act && o.poi >= 0).map(o => o.poi))
  const prefs = poseFor(a)
  for (const pose of prefs) {
    const free = pois.map((p, i) => i).filter(i => pois[i][2] === pose && !taken.has(i) && i !== act.poi)
    if (free.length) return free[Math.floor(Math.random() * free.length)]
  }
  const any = pois.map((p, i) => i).filter(i => !taken.has(i))
  return any.length ? any[Math.floor(Math.random() * any.length)] : -1
}

function applyPose (act) {
  const pois = roomPois[st.focus.key]
  const pose = act.poi >= 0 ? pois[act.poi][2] : 'stand'
  act.el.classList.toggle('lie', pose === 'lie')
  act.el.classList.toggle('sitting', pose === 'sit')
  place(act)
  act.mood = pose === 'work' ? 'work' : pose === 'stand' ? (act.a.mood === 'sleep' && act.a.label === tr('เสร็จแล้ว') ? 'cheer' : 'think') : 'rest'
  setAnim(act, act.mood)
}

function goPoi (act, idx, instant) {
  act.poi = idx
  const [x, y] = idx >= 0 ? focusPoiXY(st.focus.key, idx) : [doorSpot[0] * st.focus.FS, 470 * st.focus.FS]
  act.el.classList.remove('lie', 'sitting')
  if (instant) {
    act.x = x
    act.y = y
    place(act)
    applyPose(act)
  } else {
    walk(act, [[x, y]], () => applyPose(act))
  }
}

function renderRoomFocus () {
  const key = st.roomFocus
  const want = actorsFor().filter(a => a.room === key)
  const fresh = st.drawn !== 'focus:' + key || !st.focus || !st.focus.stage.isConnected
  if (fresh) {
    st.drawn = 'focus:' + key
    st.actors = {}
    const b = sceneBoard(FW, FH, 'focus')
    const FS = 1500 / RW
    const stageEl = box('focusRoom', 10, 6, 1500, RH * FS)
    const screen = composeRoom(stageEl, key, FS, true)
    if (screen) {
      screen.classList.add('live')
      screen.querySelector('.scrBody').replaceChildren(h('p', { class: 'scrNote' }, want.length ? tr('กำลังโหลดข้อความ…') : tr('ไม่มี agent ในห้องนี้')))
      const tv = screen.closest('.screenWrap') || screen
      tv.classList.add('tvLive')
      tv.title = tr('คลิกเพื่อขยายจอ')
      tv.setAttribute('role', 'button')
      tv.tabIndex = 0
      tv.append(h('span', { class: 'zoomBadge' }, tr('⤢ ขยาย')))
      tv.addEventListener('click', () => openScreenPop(key))
      tv.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openScreenPop(key) } })
    }
    b.append(stageEl)
    b.append(box('focusHint', 10, RH * FS - 34, 1500, 0))
    b.querySelector('.focusHint').textContent = tr('ลาก agent ไปวางที่โซฟา เก้าอี้ เปล หรือจุดไหนก็ได้ · คลิก agent เพื่อเปิดแชต · คลิกจอเพื่อขยาย · ← หรือ Esc กลับ')
    st.focus = { key, stage: stageEl, FS, actors: {} }
    tickClocks()
  }
  const F = st.focus
  for (const a of want) {
    let act = F.actors[a.id]
    if (!act) {
      act = F.actors[a.id] = { id: a.id, kind: a.kind, char: charFor(a.id), poi: -1, sig: '', a }
      act.el = actorNode(a)
      F.stage.append(act.el)
      wireActor(act)
      act.x = doorSpot[0] * F.FS
      act.y = doorSpot[1] * F.FS
    }
    act.a = a
    const sig = [a.mood, a.label, a.name, a.canTake, st.sel && st.sel.id === a.id].join('|')
    if (sig !== act.sig) {
      const moodChanged = act.sig && act.sig.split('|')[0] !== a.mood
      act.sig = sig
      fillActor(act, a)
      act.el.classList.add('big')
      if (act.tag) act.tag.remove()
      act.tag = act.el.querySelector('.tag')
      act.tag.classList.add('big')
      act.tagW = 0
      F.stage.append(act.tag)
      if (act.poi >= 0) place(act)
      if (act.poi >= 0) applyPose(act)
      if (moodChanged && !act.dragging) { act.nextMove = 0 }
    }
    if (act.poi < 0 && !act.walking) {
      goPoi(act, pickPoi(act, a), fresh)
      act.nextMove = Date.now() + 6000 + Math.random() * 8000
    }
  }
  for (const id in F.actors) {
    if (want.some(a => a.id === id)) continue
    const act = F.actors[id]
    if (act.leaving) continue
    act.leaving = true
    act.poi = -1
    walk(act, [[doorSpot[0] * F.FS, doorSpot[1] * F.FS]], () => { act.el.remove(); if (act.tag) act.tag.remove(); delete F.actors[id]; placeTags() })
  }
  updateMonitor(want)
}

function roamTick () {
  const F = st.focus
  if (!F || !F.stage.isConnected || st.drawn !== 'focus:' + F.key) return
  const now = Date.now()
  for (const id in F.actors) {
    const act = F.actors[id]
    if (act.walking || act.dragging || act.leaving || now < (act.nextMove || 0)) continue
    const work = act.a.mood === 'work'
    const idx = pickPoi(act, act.a)
    if (idx >= 0 && !(work && act.poi >= 0 && roomPois[F.key][act.poi][2] === 'work' && Math.random() < 0.7)) goPoi(act, idx, false)
    act.nextMove = now + (work ? 20000 : 8000) + Math.random() * (work ? 20000 : 9000)
  }
}
setInterval(roamTick, 1000)

function wireActor (act) {
  const el = act.el
  el.classList.add('big')
  el.style.touchAction = 'none'
  let start = null
  el.addEventListener('pointerdown', e => {
    if (e.button !== 0) return
    start = { x: e.clientX, y: e.clientY }
    el.setPointerCapture(e.pointerId)
  })
  el.addEventListener('pointermove', e => {
    if (!start) return
    // A finger drifts several pixels during a tap; past 6px a tap became a drag and no details opened.
    const slop = e.pointerType === 'touch' ? 16 : 6
    if (!act.dragging && Math.hypot(e.clientX - start.x, e.clientY - start.y) < slop) return
    act.dragging = true
    act.route = []
    el.classList.add('dragging')
    el.classList.remove('lie', 'sitting')
    const rect = st.focus.stage.getBoundingClientRect()
    const k = rect.width / 1500
    act.x = Math.max(30, Math.min(1470, (e.clientX - rect.left) / k))
    act.y = Math.max(120, Math.min(RH * st.focus.FS, (e.clientY - rect.top) / k + 50))
    place(act)
    setAnim(act, 'cheer')
  })
  const end = () => {
    if (!start) return
    start = null
    if (!act.dragging) return
    act.dragging = false
    act.justDragged = Date.now()
    el.classList.remove('dragging')
    // snap to the nearest free spot within reach, else stand where dropped
    const pois = roomPois[st.focus.key]
    const taken = new Set(Object.values(st.focus.actors).filter(o => o !== act && o.poi >= 0).map(o => o.poi))
    let best = -1
    let bestD = 120
    pois.forEach((p, i) => {
      if (taken.has(i)) return
      const d = Math.hypot(p[0] * st.focus.FS - act.x, p[1] * st.focus.FS - act.y)
      if (d < bestD) { best = i; bestD = d }
    })
    if (best >= 0) goPoi(act, best, true)
    else { act.poi = -2; act.el.classList.remove('lie', 'sitting'); act.mood = 'think'; setAnim(act, 'think') }
    act.nextMove = Date.now() + 60000
  }
  el.addEventListener('pointerup', end)
  el.addEventListener('pointercancel', end)
  el.addEventListener('click', e => {
    if (act.justDragged && Date.now() - act.justDragged < 400) { e.stopPropagation(); return }
    select(act.kind, act.id)
  })
  el.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); select(act.kind, act.id) } })
}

// real wall clocks
function clockEl (x, y, w) {
  const c = box('clockLive', x, y, w, w)
  c.append(h('i', { class: 'hh' }), h('i', { class: 'mh' }), h('i', { class: 'sh' }), h('b'))
  for (let k = 0; k < 12; k++) {
    const t = h('u')
    t.style.transform = 'rotate(' + (k * 30) + 'deg)'
    c.append(t)
  }
  return c
}
function tickClocks () {
  const d = new Date()
  const m = d.getMinutes() + d.getSeconds() / 60
  const hr = (d.getHours() % 12) + m / 60
  for (const c of document.querySelectorAll('.clockLive')) {
    c.querySelector('.hh').style.transform = 'rotate(' + (hr * 30) + 'deg)'
    c.querySelector('.mh').style.transform = 'rotate(' + (m * 6) + 'deg)'
    c.querySelector('.sh').style.transform = 'rotate(' + (d.getSeconds() * 6) + 'deg)'
  }
}
setInterval(tickClocks, 1000)

// day phases
const phases = [
  { key: 'night', from: 0, label: tr('ดึก'), icon: '🌙' },
  { key: 'morning', from: 6, label: tr('เช้า'), icon: '🌅' },
  { key: 'late', from: 9, label: tr('สาย'), icon: '🌤' },
  { key: 'afternoon', from: 12, label: tr('บ่าย'), icon: '☀️' },
  { key: 'evening', from: 16, label: tr('เย็น'), icon: '🌇' },
  { key: 'night', from: 19, label: tr('ดึก'), icon: '🌙' }
]
function applyPhase () {
  const d = new Date()
  const hr = d.getHours()
  let p = phases[0]
  for (const x of phases) if (hr >= x.from) p = x
  document.body.dataset.phase = p.key
  const chip = $('phaseChip')
  if (chip) chip.textContent = p.icon + ' ' + p.label + ' · ' + d.toLocaleTimeString('th-TH', { hour: '2-digit', minute: '2-digit' })
}
setInterval(applyPhase, 30000)

function monitorCards (want, msgsById, big) {
  if (!want.length) return [h('p', { class: 'scrNote' }, tr('ไม่มี agent ในห้องนี้'))]
  return want.map(a => h('div', big ? { class: 'scrItem ' + a.mood, tabindex: 0, onclick: e => { e.stopPropagation(); st.tab = 'chat'; select(a.kind, a.id) } } : { class: 'scrItem ' + a.mood },
    h('div', { class: 'scrTop' }, h('b', null, a.name), h('em', null, a.kind === 'sub' ? 'sub-agent' : a.label)),
    ...(big && a.kind === 'session' ? openReviewsOf(a.id).map(r => h('button', { type: 'button', class: 'pri revOpen', onclick: e => { e.stopPropagation(); $('screenPop').close(); openReview(r.id) } }, tr('🧪 เปิดตรวจงาน: ') + r.title)) : []),
    ...((a.watches || []).filter(w => w.ci).slice(0, big ? 6 : 2).map(w => h('div', { class: 'scrJob' }, h('i'), h('b', null, w.label), h('span', null, (w.event || tr('รอผล…')) + (w.eventAt && !w.eventAt.startsWith('0001') ? ' · ' + fmtTime(w.eventAt) : ''))))),
    ...((msgsById[a.id] || []).slice(big ? -3 : -1).map(m => h('div', { class: 'scrMsg ' + m.role }, h('span', null, (m.role === 'user' ? tr('ผู้ใช้') : 'agent') + ' · ' + fmtTime(m.at)), big ? mdBody(m.text) : h('p', null, m.text.slice(0, 160)), big ? msgImages(m.text) : null)))))
}

// The live wall shows two lines in the room, so the Testing room puts the
// waiting reviews first and the agents after them.
function liveWallCards (key, want) {
  const open = key === 'test' ? (st.reviews || []).filter(r => r.status === 'open') : []
  const head = open.length ? [h('div', { class: 'scrItem revWall' }, h('b', null, '🧪 ' + tr('งานรอตรวจ (') + open.length + ')'), h('p', null, open[0].title))] : []
  return [...head, ...monitorCards(want, st.monMsgs, false)]
}

async function updateMonitor (want) {
  const now = Date.now()
  if (st.monAt && now - st.monAt < 3500) return
  st.monAt = now
  st.monMsgs = st.monMsgs || {}
  st.monSeen = st.monSeen || {}
  for (const a of want.slice(0, 10)) {
    if (a.kind !== 'session') continue
    // only agents with something new since the last read
    const sess = st.snap.sessions.find(x => x.sessionId === a.id)
    const mark = sess ? (sess.lastEventAt || '') + '|' + sess.status : ''
    if (st.monMsgs[a.id] && st.monSeen[a.id] === mark) continue
    st.monSeen[a.id] = mark
    try {
      const res = await api('/sessions/' + encodeURIComponent(a.id) + '/messages?limit=3')
      st.monMsgs[a.id] = res.messages || []
    } catch (e) { /* keep last */ }
  }
  const body = document.querySelector('.wallScreen.live .scrBody')
  if (body) body.replaceChildren(...liveWallCards(st.roomFocus, want))
  if ($('screenPop').open && st.popRoom === st.roomFocus) {
    $('screenPopBody').replaceChildren(...popContent(st.popRoom, want))
    drawPopComposer(want)
  }
}

// popContent: the Testing room's screen always starts with the reviews
function popContent (key, want) {
  return [...(key === 'test' ? [reviewList(true)] : []), ...monitorCards(want, st.monMsgs || {}, true)]
}

// A picture copied to the clipboard is attached like a picked file; text pastes
// through untouched.
function pasteImages (e, thumbs) {
  const files = [...(e.clipboardData ? e.clipboardData.items : [])].filter(i => i.kind === 'file').map(i => i.getAsFile())
  if (!files.length) return
  e.preventDefault()
  addFiles(files, thumbs)
}

// The monitor's own composer. It is built into the dialog footer and left alone
// by the refresh above, so what you are typing survives the room updating under
// you — only a change of who is in the room rebuilds it.
function drawPopComposer (want) {
  const foot = $('screenPopFoot')
  const targets = want
    .filter(a => a.kind === 'session' && a.canTake)
    .map(a => st.snap.sessions.find(s => s.sessionId === a.id))
    .filter(Boolean)
  const sig = targets.map(s => s.sessionId).join(',')
  if (foot.dataset.sig === sig) return
  foot.dataset.sig = sig
  if (!targets.length) {
    foot.replaceChildren(h('p', { class: 'note' }, tr('ไม่มี agent ในห้องนี้ที่รับข้อความจากเว็บได้')))
    return
  }
  const ta = h('textarea', { rows: 1, maxlength: 4000, placeholder: tr('พิมพ์ถึง agent ในห้องนี้…'), 'aria-label': tr('ข้อความ') })
  ta.addEventListener('input', () => { ta.style.height = 'auto'; ta.style.height = Math.min(160, ta.scrollHeight + 2) + 'px' })
  const thumbs = h('div', { class: 'thumbs' })
  const pick = targets.length > 1
    ? h('select', { 'aria-label': tr('เลือก agent ที่จะส่งถึง') }, targets.map(s => h('option', { value: s.sessionId }, s.displayName)))
    : null
  const targetOf = () => (pick ? targets.find(s => s.sessionId === pick.value) : targets[0]) || targets[0]
  const file = h('input', { type: 'file', accept: 'image/png,image/jpeg,image/gif,image/webp', multiple: true, hidden: true })
  file.addEventListener('change', () => { addFiles(file.files, thumbs); file.value = '' })
  ta.addEventListener('paste', e => pasteImages(e, thumbs))
  const go = h('button', { type: 'button', class: 'pri' }, tr('ส่ง'))
  const send = () => { sendOnce(ta, go, () => sendChat(targetOf(), ta, thumbs)).then(() => { ta.style.height = '' }); return false }
  go.addEventListener('click', send)
  ta.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.shiftKey && !isPhone()) { e.preventDefault(); if (!e.repeat) send() }
  })
  const last = [...(st.monMsgs[targets[0].sessionId] || [])].reverse().find(m => m.role === 'assistant')
  const sugs = suggestFrom(last ? last.text : '').slice(0, 3)
  foot.replaceChildren(
    sugs.length ? h('div', { class: 'suggest' }, sugs.map(v => h('button', { type: 'button', class: 'sug', onclick: () => { ta.value = v; ta.focus() } }, v))) : '',
    h('div', { class: 'spAsk' },
      pick || '',
      h('button', { type: 'button', class: 'ghost clip', 'aria-label': tr('แนบรูป'), onclick: () => file.click() }, '📎'),
      thumbs, file,
      h('div', { class: 'spTa' }, ta),
      go)
  )
  drawThumbs(thumbs)
}

// The detail sheet covers the page, and the page composer is inert under that
// modal, so a session open in the sheet carries its own composer.
function drawSheetComposer () {
  const foot = $('panelPopFoot')
  const sess = composerSession()
  const sig = sess ? sess.sessionId : ''
  if (foot.dataset.sig === sig) return
  foot.dataset.sig = sig
  foot.replaceChildren()
  foot.hidden = !sess
  if (!sess) return
  const ta = h('textarea', { rows: 1, maxlength: 4000, placeholder: tr('พิมพ์ถึง ') + sess.displayName + '…', 'aria-label': tr('ข้อความ') })
  ta.addEventListener('input', () => { ta.style.height = 'auto'; ta.style.height = Math.min(160, ta.scrollHeight + 2) + 'px' })
  const thumbs = h('div', { class: 'thumbs' })
  const file = h('input', { type: 'file', accept: 'image/png,image/jpeg,image/gif,image/webp', multiple: true, hidden: true })
  file.addEventListener('change', () => { addFiles(file.files, thumbs); file.value = '' })
  ta.addEventListener('paste', e => pasteImages(e, thumbs))
  const go = h('button', { type: 'button', class: 'pri' }, tr('ส่ง'))
  const send = () => {
    const now = composerSession()
    if (now) sendOnce(ta, go, () => sendChat(now, ta, thumbs)).then(() => { ta.style.height = '' })
    return false
  }
  go.addEventListener('click', send)
  foot.append(h('div', { class: 'spAsk' },
    h('button', { type: 'button', class: 'ghost clip', 'aria-label': tr('แนบรูป'), onclick: () => file.click() }, '📎'),
    thumbs, file,
    h('div', { class: 'spTa' }, ta),
    go))
  drawThumbs(thumbs)
}

function openScreenPop (key) {
  st.popRoom = key
  const R = roomDefs[key]
  const t = R.screen ? R.screen[4] : ''
  $('screenPopTitle').textContent = t.includes(R.th) ? t : t + ' · ' + R.th
  const want = actorsFor().filter(a => a.room === key)
  $('screenPopBody').replaceChildren(...popContent(key, want))
  $('screenPopFoot').dataset.sig = ''
  drawPopComposer(want)
  $('screenPop').showModal()
}


function renderInterior () {
  const fresh = st.drawn !== 'house:' + st.house || !st.actorLayer || !st.actorLayer.isConnected
  if (fresh) {
    st.drawn = 'house:' + st.house
    st.actors = {}
    st.actorLayer = drawInterior()
  }
  fillRoomTasks()
  fillRoomSignals()
  const layer = st.actorLayer
  const want = actorsFor()
  const taken = {}
  for (const id in st.actors) {
    const act = st.actors[id]
    if (want.some(a => a.id === id) && act.room) (taken[act.room] = taken[act.room] || new Set()).add(act.seat)
  }
  for (const a of want) {
    let act = st.actors[a.id]
    const isNew = !act
    if (isNew) {
      act = st.actors[a.id] = { id: a.id, x: frontDoor.x, y: frontDoor.y, room: null, seat: -1, sig: '', char: charFor(a.id) }
      act.el = actorNode(a)
      act.el.addEventListener('click', () => select(act.kind, act.id))
      act.el.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); select(act.kind, act.id) } })
      layer.append(act.el)
    }
    act.kind = a.kind
    const sig = [a.mood, a.label, a.name, a.canTake, st.sel && st.sel.id === a.id].join('|')
    if (sig !== act.sig) {
      act.sig = sig
      fillActor(act, a)
    }
    if (act.room !== a.room) {
      const used = taken[a.room] = taken[a.room] || new Set()
      const seats = seatsOf(a.room)
      let seat = seats.findIndex((_, i) => !used.has(i))
      if (seat < 0) seat = seats.length + (hash(a.id) % 8)
      used.add(seat)
      const r = rooms2[a.room]
      const target = seats[seat] || [r.x + 40 + (seat % 8) * 40, r.y + CH - 20]
      if (fresh) {
        // entering the house: everyone is already in place — no replay
        act.x = target[0]
        act.y = target[1]
        place(act)
      } else {
        walk(act, routeTo(act, a.room, target))
      }
      act.room = a.room
      act.seat = seat
    }
  }
  for (const id in st.actors) {
    if (want.some(a => a.id === id)) continue
    const act = st.actors[id]
    if (act.leaving) continue
    act.leaving = true
    walk(act, routeTo(act, 'out'), () => { act.el.remove(); delete st.actors[id] })
  }
}

const doorTimers = {}
function openDoor (el, id) {
  if (!el) return
  el.classList.add('open')
  clearTimeout(doorTimers[id])
  doorTimers[id] = setTimeout(() => el.classList.remove('open'), 650)
}

function doorsNear (x, y) {
  for (const key in rooms2) {
    const r = rooms2[key]
    const dy = r.top ? r.y : r.y + CH
    if (Math.abs(x - r.door) < 44 && Math.abs(y - dy) < 46) openDoor(r.doorEl, key)
  }
  const sd = document.querySelector('.sideDoor')
  if (sd && Math.abs(x - frontDoor.x) < 60 && Math.abs(y - hallA) < 50) openDoor(sd, 'side')
}

// tagY is how far below an agent's spot its name starts: under the feet.
const tagH = 40
function tagY (act) {
  const c = act.el.classList
  return c.contains('lie') ? -40 : c.contains('sitting') ? 24 : 2
}

// placeTags puts the names of a room's agents in one layer over everybody, so
// neither their own feet nor a neighbour covers one, and stacks those that
// would lie on each other.
function placeTags () {
  const F = st.focus
  const done = []
  const acts = Object.values(F.actors).filter(a => a.tag && a.vx !== undefined).sort((a, b) => b.y - a.y)
  for (const act of acts) {
    const w = act.tagW = act.tagW || act.tag.offsetWidth || 160
    const x = Math.max(w / 2 + 8, Math.min(1492 - w / 2, act.vx))
    let y = act.vy + tagY(act)
    for (const o of done) if (Math.abs(o.x - x) < (o.w + w) / 2 && Math.abs(o.y - y) < tagH) y = o.y - tagH
    done.push({ x, y, w })
    act.tag.style.transform = 'translate(' + (x - w / 2).toFixed(1) + 'px,' + y.toFixed(1) + 'px)'
  }
}

function place (act) {
  if (act.tag) {
    // keep the agent, and the name under it, inside the room
    const c = act.el.classList
    act.vx = Math.max(90, Math.min(1410, act.x))
    act.vy = Math.min(RH * st.focus.FS - tagH - (c.contains('lie') ? 0 : 26), act.y)
    act.el.style.transform = 'translate(' + act.vx.toFixed(1) + 'px,' + act.vy.toFixed(1) + 'px)'
    act.el.style.zIndex = Math.round(act.vy)
    placeTags()
    return
  }
  act.el.style.transform = 'translate(' + act.x.toFixed(1) + 'px,' + act.y.toFixed(1) + 'px)'
  act.el.style.zIndex = Math.round(act.y)
}

function walk (act, route, done) {
  act.route = route.slice()
  act.done = done
  act.walking = true
  act.el.classList.add('walking')
  if (act.anim) return
  let last = performance.now()
  const step = now => {
    const dt = Math.min(0.05, (now - last) / 1000)
    last = now
    let budget = (reduceMotion() ? 5000 : 170) * dt
    while (budget > 0 && act.route.length) {
      const [tx, ty] = act.route[0]
      const dx = tx - act.x
      const dy = ty - act.y
      const d = Math.hypot(dx, dy)
      if (Math.abs(dx) > 0.5) {
        act.walkDir = dx < 0 ? 'walkL' : 'walkR'
        setAnim(act, act.walkDir)
      }
      if (d <= budget) {
        act.x = tx
        act.y = ty
        act.route.shift()
        budget -= d
      } else {
        act.x += dx / d * budget
        act.y += dy / d * budget
        budget = 0
      }
    }
    place(act)
    doorsNear(act.x, act.y)
    if (act.route.length && act.el.isConnected) {
      act.anim = requestAnimationFrame(step)
    } else {
      act.anim = null
      act.walking = false
      act.el.classList.remove('walking')
      setAnim(act, act.mood || 'rest')
      if (act.done) act.done()
    }
  }
  act.anim = requestAnimationFrame(step)
}

function reduceMotion () {
  try { return window.matchMedia('(prefers-reduced-motion: reduce)').matches } catch (e) { return false }
}

// ---------- list view ----------
// The session list is the home screen: one card per main agent. It is the only
// view that does not run out of room, which the five-house map did as soon as a
// sixth session started.
// The role the admin gave: admin (on the guard's admin list) or the room's duty.
const roleIcon = { admin: '🛡️', frontend: '🎨', backend: '⚙️', fullstack: '🧩', tester: '🧪' }
function roleBadge (role) {
  if (!roleIcon[role]) return null
  return h('span', { class: 'roleBadge role-' + role, title: tr('บทบาท: ') + dutyLabel(role) }, roleIcon[role] + ' ' + dutyLabel(role))
}

function sessionCard (sess) {
  const waiting = sess.status === 'needsInput'
  const resting = sess.status === 'idle' || sess.status === 'completed'
  const tone = !sess.live ? 'off' : waiting ? 'ask' : resting ? 'idle' : 'work'
  const state = !sess.live ? statusLabel.ended
    : waiting ? tr('รอคุณตอบ')
      : sess.status === 'completed' ? tr('เสร็จแล้ว')
        : sess.status === 'idle' ? tr('ว่าง')
          : activityLabel[sess.activityType]
  const subs = (sess.subAgents || []).length
  const revs = openReviewsOf(sess.sessionId).length
  const ava = h('div', { class: 'ava' }, avatarFace(sess))
  ava.style.background = 'color-mix(in srgb, ' + agentColor(sess) + ' 18%, transparent)'
  return h('button', {
    type: 'button',
    class: 'sCard' + (sess.live ? '' : ' off'),
    onclick: () => select('session', sess.sessionId)
  },
  ava,
  h('div', { class: 'sMain' },
    h('b', null, nameOf(sess), roleBadge(sess.role)),
    h('span', { class: 'sMeta' }, runtimeName[sess.runtimeType] + ' · ' + (sess.repoId || '—') + (sess.gitBranch ? ' · ' + sess.gitBranch : ''))),
  h('div', { class: 'sRight' },
    h('em', { class: 'pill ' + tone }, state),
    h('span', { class: 'sNums' },
      pendingOf(sess.sessionId).length ? '⏳ ' + tr('รอส่ง ') + pendingOf(sess.sessionId).length + ' · ' : '',
      subs ? subs + tr(' ตัวช่วย') : '',
      subs && revs ? ' · ' : '',
      revs ? revs + tr(' รอตรวจ') : '',
      (subs || revs) ? ' · ' : '',
      ago(sess.lastEventAt))))
}

function renderList () {
  const box = $('list')
  const rowR = run => h('tr', { tabindex: 0, onclick: () => select('run', run.id), onkeydown: e => { if (e.key === 'Enter') select('run', run.id) } },
    h('td', { class: 'mono' }, run.id),
    h('td', null, run.workspace),
    h('td', null, run.lost ? tr('ขาดการเชื่อมต่อ') : (runLabel[run.status] || run.status)),
    h('td', null, run.task.slice(0, 120)))
  const head = (...cols) => h('thead', null, h('tr', null, cols.map(c => h('th', null, c))))
  const live = st.snap.sessions.filter(x => x.live)
  const ended = st.snap.sessions.filter(x => !x.live)
  box.replaceChildren(
    h('h3', null, tr('Agent ที่ออนไลน์ (') + live.length + ')'),
    live.length ? h('div', { class: 'sCards' }, live.map(sessionCard)) : h('p', { class: 'note' }, tr('ยังไม่มี agent ที่ออนไลน์')),
    isUser() ? '' : h('h3', null, tr('งาน agcmd (') + st.snap.runs.length + ')'),
    isUser() ? '' : h('table', { class: 'runs' }, head('Run', 'Workspace', tr('สถานะ'), tr('งาน')), h('tbody', null, st.snap.runs.map(rowR))),
    h('h3', null, tr('Session ที่จบแล้ววันนี้ (') + ended.length + ')'),
    ended.length ? h('div', { class: 'sCards' }, ended.map(sessionCard)) : h('p', { class: 'note' }, tr('ยังไม่มี'))
  )
}

// ---------- inspector ----------
function targetFor (kind, id) {
  st.blockReason = ''
  if (kind === 'session') {
    const sess = st.snap.sessions.find(x => x.sessionId === id)
    if (sess && sess.dispatchVia) return sess.dispatchVia
    if (sess && (sess.readOnly || sess.protected) && sess.messageBlocked) { st.blockReason = sess.messageBlocked; return '' }
    st.blockReason = (sess ? sess.displayName : tr('agent นี้')) + tr(' ไม่ได้รันใน tmux ของ agcmd — รับงานจากเว็บไม่ได้ (ดูได้อย่างเดียว)')
    return ''
  }
  const run = st.snap.runs.find(x => x.id === id)
  const w = run && st.snap.workspaces.find(x => x.name === run.workspace)
  return w && w.agentReady ? w.name : ''
}

// A phone has no room for a 380px column beside the stage, so the detail
// opens as a sheet there instead. It is the same panel element either way —
// openPanelSheet moves it rather than building a second one.
const isPhone = () => window.matchMedia('(max-width: 700px)').matches

async function select (kind, id) {
  if (isPhone()) openPanelSheet()
  st.target = targetFor(kind, id)
  setTimeout(updateComposerHint, 0)
  if (!st.sel || st.sel.id !== id) { st.chatSig = ''; st.inspKey = ''; st.attach = [] }
  st.sel = { kind, id }
  if (kind === 'session' || kind === 'sub') { st.follow = id; clearTimeout(st.followTimer); st.followTimer = null }
  st.detail = null
  st.runLog = null
  render()
  try {
    if (kind === 'session') st.detail = await api('/sessions/' + encodeURIComponent(id))
    else st.runLog = (await api('/runs/' + encodeURIComponent(id) + '/log')).lines
  } catch (e) { /* inspector shows what the snapshot has */ }
  if (st.sel && st.sel.id === id) renderInspector()
}

function renderInspector () {
  const box = $('insp')
  const sel = st.sel
  if (sel && sel.kind === 'session') {
    const sess = st.snap.sessions.find(x => x.sessionId === sel.id)
    if (sess) {
      const tab = st.tab || 'chat'
      const key = [sess.sessionId, charFor(sess.sessionId), tab, sess.prompt ? sess.prompt.question + sess.prompt.options.length + (sess.prompt.ask ? '|' + sess.prompt.ask.active + (sess.prompt.ask.review ? 'r' : '') + (sess.prompt.ask.questions || []).length + sess.prompt.ask.tabs.map(t => t.answered ? 1 : 0).join('') : '') : '', sess.status, sess.activityType, sess.messageVia, sess.stopVia, sess.live, sess.role, sess.pinnable].join('|')
      if (tab === 'chat' && st.inspKey === key && box.querySelector('.chatLog')) {
        updateChat()
        return
      }
      st.inspKey = key
      box.replaceChildren(...sessionPanel(sess, tab).flat().filter(Boolean))
      if (tab === 'chat') updateChat(true)
      return
    }
  }
  st.inspKey = ''
  if (!sel && st.filter) return box.replaceChildren(...filterPanel(st.filter).flat().filter(Boolean))
  if (sel && sel.kind === 'sub') {
    const [pid, sid] = sel.id.split(':')
    const parent = st.snap.sessions.find(x => x.sessionId === pid)
    const sa = parent && (parent.subAgents || []).find(x => x.id === sid)
    if (sa) return box.replaceChildren(...subPanel(parent, sa).flat().filter(Boolean))
  }
  if (sel && sel.kind === 'run') {
    const run = st.snap.runs.find(x => x.id === sel.id)
    if (run) return box.replaceChildren(...runPanel(run).flat().filter(Boolean))
  }
  box.replaceChildren(...overviewPanel().flat().filter(Boolean))
}

function back () { stopFollow(); st.sel = null; st.blockReason = ''; st.chat = null; st.inspKey = ''; render(); updateComposerHint() }

function subPanel (parent, sa) {
  return [
    h('button', { type: 'button', class: 'ghost', onclick: back }, tr('← ภาพรวม')),
    h('h3', null, tr('Sub-agent · ดูได้อย่างเดียว')),
    h('h2', null, sa.description),
    h('dl', { class: 'kv' },
      h('dt', null, tr('ชนิด')), h('dd', null, sa.type || '—'),
      h('dt', null, tr('สถานะ')), h('dd', null, sa.status === 'done' ? tr('เสร็จแล้ว') : tr('กำลังทำ · ') + (activityLabel[sa.activityType] || sa.activityType)),
      h('dt', null, tr('เครื่องมือล่าสุด')), h('dd', null, sa.lastTool || '—'),
      h('dt', null, tr('อัปเดต')), h('dd', null, ago(sa.lastEventAt)),
      h('dt', null, tr('agent แม่')), h('dd', null, parent.displayName)
    ),
    sa.task ? h('div', { class: 'subBlock' }, h('h3', null, tr('งานที่ได้รับจาก agent แม่')), h('p', null, sa.task)) : null,
    sa.latest ? h('div', { class: 'subBlock' }, h('h3', null, tr('รายงานล่าสุด')), h('p', null, sa.latest)) : null,
    h('p', { class: 'note' }, tr('sub-agent ทำงานแทน agent แม่ และรายงานผลกลับไปที่ agent แม่ — สั่งงานแทรกไม่ได้ ถ้าจะสั่งให้พิมพ์ถึง agent แม่')),
    h('button', { type: 'button', class: 'pri', onclick: () => { st.tab = 'chat'; select('session', parent.sessionId) } }, tr('ไปที่ agent แม่: ') + parent.displayName)
  ]
}

function subCards (sess) {
  const subs = sess.subAgents || []
  if (!subs.length) return null
  return h('div', { class: 'subCards' }, subs.map(sa => h('button', { type: 'button', class: 'subCard ' + (sa.status === 'done' ? 'done' : 'run'), onclick: () => select('sub', sess.sessionId + ':' + sa.id) },
    h('div', { class: 'subTop' }, h('i'), h('b', null, sa.description || sa.type), h('em', null, sa.status === 'done' ? tr('เสร็จแล้ว') : (activityLabel[sa.activityType] || tr('กำลังทำ')) + (sa.lastTool ? ' · ' + sa.lastTool : ''))),
    sa.type ? h('small', null, sa.type + tr(' · อัปเดต ') + ago(sa.lastEventAt)) : null,
    sa.task ? h('p', null, h('u', null, tr('งาน: ')), sa.task) : null,
    sa.latest ? h('p', { class: 'latest' }, h('u', null, tr('ล่าสุด: ')), sa.latest) : null)))
}

function subList (sess) {
  const subs = sess.subAgents || []
  if (!subs.length) return null
  return h('div', { class: 'tl' }, h('h3', null, tr('Sub-agents ที่ช่วยอยู่ (') + subs.length + tr(') · ดูได้อย่างเดียว')),
    subs.map(sa => h('div', null, h('span', null, sa.status === 'done' ? '✓' : '●'), h('span', null, (sa.type ? sa.type + ' · ' : '') + sa.description + (sa.lastTool ? ' — ' + sa.lastTool : '')))))
}

// The admin puts one of the main service's own sessions on the guard's admin
// list by hand, or takes it off (adminlist.go). Room sessions get admin from
// their room's duty, so the service marks only its own as pinnable; a user
// never sees this.
function adminToggle (sess) {
  if (isUser() || st.as || !sess.pinnable) return null
  const on = sess.role !== 'admin'
  return h('button', { type: 'button', class: on ? 'pill work' : 'pill ask', title: tr('รายชื่อ admin ที่ guard ของเครื่องอ่าน (protected.json)'), onclick: () => setSessionAdmin(sess, on) }, on ? tr('ตั้งเป็น admin') : tr('เลิกเป็น admin'))
}
async function setSessionAdmin (sess, on) {
  const body = on
    ? tr('session นี้จะอยู่ในรายชื่อ admin (protected.json) ที่ guard ของเครื่องอ่าน ผู้ใช้จะเห็นแค่ว่ามี session นี้ และ /clear ใน session เดิมจะยังเป็น admin ต่อ')
    : tr('session นี้จะออกจากรายชื่อ admin ที่ guard อ่าน')
  if (!await confirmBox(on ? tr('ตั้งเป็น admin') : tr('เลิกเป็น admin'), body, nameOf(sess), on ? tr('ตั้งเป็น admin') : tr('เลิกเป็น admin'))) return
  act('/sessions/' + encodeURIComponent(sess.sessionId) + '/admin', { on },
    r => r.admin === on ? nameOf(sess) + (on ? tr(' เป็น admin แล้ว') : tr(' ไม่เป็น admin แล้ว')) : nameOf(sess) + tr(' ยังเป็น admin เพราะอยู่ใน workspace ที่ป้องกันไว้ และบทบาทของคุณคือ admin'))
}

function sessionHeader (sess) {
  const ava = h('button', { type: 'button', class: 'ava avaPick', title: tr('เปลี่ยนตัวละคร'), 'aria-label': tr('เปลี่ยนตัวละคร'), onclick: () => openCharPicker(sess) }, avatarFace(sess))
  ava.style.background = 'color-mix(in srgb, ' + agentColor(sess) + ' 18%, transparent)'
  const stateDot = h('i')
  stateDot.style.background = !sess.live ? 'var(--line)' : sess.status === 'running' ? 'var(--brand)' : sess.status === 'needsInput' ? 'var(--amber)' : 'var(--muted)'
  return [
    h('div', { class: 'sessTop' },
      h('button', { type: 'button', class: 'ghost', onclick: back }, tr('← ภาพรวม')),
      h('div', { class: 'who' }, ava, h('div', null,
        h('h2', { class: 'nameRow' }, h('span', null, nameOf(sess)),
          sess.readOnly || sess.protected && isUser() ? null : h('button', { type: 'button', class: 'ghost renameBtn', title: tr('เปลี่ยนชื่อที่แสดงในเว็บ'), 'aria-label': tr('เปลี่ยนชื่อ'), onclick: () => renameSession(sess) }, '✎')),
        h('p', null, runtimeName[sess.runtimeType] + ' · ' + (sess.repoId || '—')))),
      h('div', { class: 'state' }, stateDot, stateText(sess),
        !sess.live && sess.runtimeType === 'claudeCode' && sess.cwd ? h('button', { type: 'button', class: 'pill work', title: tr('เปิดบทสนทนานี้ต่อ โดยไม่พิมพ์ข้อความเข้าไป'), onclick: () => doResume(sess) }, tr('เปิดต่อ')) : null,
        adminToggle(sess),
        (sess.subAgents || []).length
        ? h('button', { type: 'button', class: 'pill work subToggle' + (st.subsOpen ? ' on' : ''), 'aria-expanded': String(!!st.subsOpen), onclick: () => { st.subsOpen = !st.subsOpen; st.inspKey = ''; renderInspector() } }, '+' + sess.subAgents.length + ' sub-agent ' + (st.subsOpen ? '▴' : '▾'))
        : null),
      modelRow(sess)),
    st.subsOpen ? subCards(sess) : null,
    ...openReviewsOf(sess.sessionId).map(r => h('button', { type: 'button', class: 'revAlert', onclick: () => openReview(r.id) }, h('b', null, tr('🧪 รอคุณตรวจงาน')), h('span', null, r.title), h('em', null, tr('เปิดตรวจ →')))),
    promptBox(sess),
    sess.doing && sess.live && sess.status === 'running' ? h('div', { class: 'doing' }, h('i'), h('span', null, sess.doing.detail), h('small', null, ago(sess.doing.at))) : null
  ]
}

// A question on the agent's screen, with its choices as buttons.
function promptBox (sess) {
  const p = sess.prompt
  if (!p || sess.readOnly || (sess.protected && isUser())) return null
  if (p.ask) return askBox(sess, p)
  const pick = o => onceFor('answer:' + sess.sessionId, async () => {
    if (!await confirmBox(tr('ตอบ agent'), p.question || '', o.n + '. ' + o.label + (o.detail ? '\n' + o.detail : ''), tr('เลือกข้อนี้'))) return
    await act('/sessions/' + encodeURIComponent(sess.sessionId) + '/answer', { option: o.n, sendId: newSendId() }, () => tr('ตอบแล้ว: ') + o.n + '. ' + o.label)
  })
  return h('div', { class: 'promptBox' },
    h('b', null, tr('agent ถามและรอคำตอบ')),
    p.question ? h('p', null, p.question) : null,
    // a menu with a search box or a slider: shown, picked in the terminal
    p.readOnly ? h('small', null, p.note || '') : null,
    ...p.options.map(o => h('button', { type: 'button', class: 'ghost', disabled: !!o.needsText || !!p.readOnly, title: o.needsText ? tr('ข้อนี้ต้องพิมพ์ข้อความ ตอบใน terminal') : '', onclick: () => pick(o) },
      h('b', null, o.n + '. ' + o.label), o.detail ? h('small', null, o.detail) : null)))
}

// Claude Code's AskUserQuestion: every question at once (radio for one
// choice, boxes for several, a line for "Type something"), one Send for all of
// them, and "Chat about this" on its own. What the user picked is kept per
// session for as long as the same questions are on screen, and dropped as
// soon as they change. The service presses the keys and checks the screen
// before each one (ask.go).
const askPicks = new Map()

// askQuestions is every question the page can answer: all of them when the
// transcript gave them, else only the one on screen.
function askQuestions (p) {
  const a = p.ask
  if (a.questions && a.questions.length) {
    return a.questions.map((q, i) => ({ tab: i, header: q.header, question: q.question, multi: !!q.multiSelect, freeText: !!q.freeText, options: q.options }))
  }
  if (a.review || a.active < 0 || a.active >= a.tabs.length) return []
  return [{
    tab: a.active,
    header: a.header,
    question: a.question,
    multi: !!a.multiSelect,
    freeText: !!a.textOption,
    options: p.options.filter(o => o.n !== a.textOption).map(o => ({ label: o.label, detail: o.detail }))
  }]
}

function askStateFor (sess, p, qs) {
  const sig = JSON.stringify([p.ask.tabs.map(t => t.header), qs.map(q => [q.tab, q.question, q.options.map(o => o.label)])])
  let st0 = askPicks.get(sess.sessionId)
  if (!st0 || st0.sig !== sig) {
    st0 = { sig, picks: {}, sendId: '', sentBody: '' }
    // what the screen already shows ticked on the question in front
    const a = p.ask
    const on = qs.find(q => q.tab === a.active)
    if (on && on.multi) {
      const sel = p.options.filter(o => o.selected && o.n !== a.textOption).map(o => o.label)
      if (sel.length) st0.picks[on.tab] = { options: sel, text: '' }
    }
    askPicks.set(sess.sessionId, st0)
  }
  return st0
}

function askAnswered (pk) { return !!pk && ((pk.options || []).length > 0 || !!(pk.text || '').trim()) }

function askBox (sess, p) {
  const a = p.ask
  // on the Submit tab the answers are in already: show them and confirm
  const qs = a.review ? [] : askQuestions(p)
  const state = askStateFor(sess, p, qs)
  const tabs = a.tabs.map(t => t.header)
  const path = '/sessions/' + encodeURIComponent(sess.sessionId) + '/answer'
  const sendBtn = h('button', { type: 'button', class: 'pri askSend' }, a.review && !qs.length ? tr('ยืนยันส่งคำตอบ') : tr('ส่งคำตอบ'))
  const note = h('small', { class: 'askNote' })
  // questions on other tabs that this page cannot show must already be answered
  const othersDone = a.tabs.every((t, i) => t.answered || qs.some(q => q.tab === i))
  const refresh = () => {
    const missing = qs.filter(q => !askAnswered(state.picks[q.tab])).map(q => q.header)
    let why = ''
    if (missing.length) why = tr('ยังไม่ได้ตอบ: ') + missing.join(', ')
    else if (!othersDone) why = tr('มีคำถามที่ต้องตอบใน terminal ก่อน')
    else if (a.review && !qs.length && a.unanswered) why = tr('ยังมีคำถามที่ยังไม่ได้ตอบ')
    sendBtn.disabled = !!why
    note.textContent = why
  }
  const body = () => {
    const answers = qs.map(q => {
      const pk = state.picks[q.tab] || {}
      return { tab: q.tab, question: q.question, options: pk.options || [], text: (pk.text || '').trim() }
    })
    return { tabs, answers, submit: othersDone }
  }
  const send = () => onceFor('answer:' + sess.sessionId, async () => {
    const ask = body()
    const summary = ask.answers.map(x => qs.find(q => q.tab === x.tab).header + ': ' + x.options.concat(x.text ? [x.text] : []).join(', ')).join('\n')
    if (!await confirmBox(tr('ตอบ agent'), tr('ส่งคำตอบทั้งหมดนี้ให้ agent'), summary || (a.summary || []).map(x => x.question + ': ' + x.answer).join('\n'), tr('ส่งคำตอบ'))) return
    // the same answers sent again reuse their id: the service then answers
    // with the first result instead of pressing the keys twice
    const key = JSON.stringify(ask)
    if (state.sentBody !== key) { state.sentBody = key; state.sendId = newSendId() }
    await sendOnce(sendBtn, sendBtn, () => act(path, { ask, sendId: state.sendId }, () => tr('ส่งคำตอบแล้ว')))
  })
  sendBtn.addEventListener('click', send)
  const chat = () => onceFor('answer:' + sess.sessionId, async () => {
    if (!await confirmBox(tr('คุยเรื่องนี้'), tr('agent จะยกเลิกคำถามชุดนี้แล้วถามว่าอยากคุยเรื่องอะไร คำตอบที่เลือกไว้จะไม่ถูกส่ง'), '', tr('คุยเรื่องนี้'))) return
    await act(path, { ask: { tabs, chat: true }, sendId: newSendId() }, () => tr('เลือก "คุยเรื่องนี้" แล้ว พิมพ์ต่อในช่องข้อความได้เลย'))
  })

  const name = 'ask-' + sess.sessionId
  const questionBox = q => {
    const pk = state.picks[q.tab] || (state.picks[q.tab] = { options: [], text: '' })
    const kind = q.multi ? 'checkbox' : 'radio'
    const set = (label, on) => {
      if (q.multi) pk.options = on ? pk.options.filter(x => x !== label).concat([label]) : pk.options.filter(x => x !== label)
      else { pk.options = on ? [label] : []; if (on) { pk.text = ''; textIn.value = '' } }
      refresh()
    }
    const textIn = h('input', { type: 'text', class: 'askText', maxlength: '500', placeholder: tr('พิมพ์คำตอบเอง'), 'aria-label': tr('พิมพ์คำตอบเอง') + ' · ' + q.header })
    textIn.value = pk.text || ''
    textIn.addEventListener('input', () => {
      pk.text = textIn.value.replace(/[\r\n]+/g, ' ')
      // one choice: typing replaces the picked option
      if (!q.multi && pk.text.trim()) {
        pk.options = []
        for (const r of box.querySelectorAll('input[type=radio]')) r.checked = false
      }
      refresh()
    })
    const tab = a.tabs[q.tab] || {}
    const box = h('fieldset', { class: 'askQ' + (q.tab === a.active ? ' on' : '') },
      h('legend', null, (tab.answered ? '☒ ' : '☐ ') + q.header, q.multi ? h('small', null, tr(' · เลือกได้หลายข้อ')) : null),
      h('p', null, q.question),
      ...q.options.map(o => {
        const inp = h('input', { type: kind, name: name + '-' + q.tab, value: o.label })
        inp.checked = pk.options.includes(o.label)
        inp.addEventListener('change', () => set(o.label, inp.checked))
        return h('label', { class: 'askOpt' }, inp,
          h('span', null, h('b', null, o.label), o.detail ? h('small', null, o.detail) : null, o.preview ? h('pre', { class: 'askPreview' }, o.preview) : null))
      }),
      q.freeText ? textIn : null)
    return box
  }

  const reviewList = a.review && !qs.length
    ? h('ul', { class: 'askSummary' }, (a.summary || []).map(x => h('li', null, h('span', null, x.question), h('b', null, '→ ' + x.answer))))
    : null
  refresh()
  return h('div', { class: 'promptBox askBox' },
    h('b', null, tr('agent ถามและรอคำตอบ')),
    a.tabs.length > 1 ? h('p', { class: 'askTabs' }, a.tabs.map(t => (t.answered ? '☒ ' : '☐ ') + t.header).join('  ·  ')) : null,
    reviewList ? h('p', null, tr('คำตอบอยู่ที่หน้าสรุปแล้ว รอกดยืนยันส่ง')) : null,
    reviewList,
    ...qs.map(questionBox),
    // the note says why Send is off, so it stays beside it in the sticky row
    h('div', { class: 'btns askBtns' }, sendBtn,
      a.chat && !a.review ? h('button', { type: 'button', class: 'ghost', onclick: chat }, tr('คุยเรื่องนี้')) : null,
      note))
}

function sessionTabs () {
  return h('div', { class: 'tabs', role: 'tablist' },
    h('button', { type: 'button', role: 'tab', class: (st.tab || 'chat') === 'chat' ? 'on' : '', 'aria-selected': String((st.tab || 'chat') === 'chat'), onclick: () => { st.tab = 'chat'; st.inspKey = ''; renderInspector() } }, tr('บทสนทนา')),
    h('button', { type: 'button', role: 'tab', class: st.tab === 'detail' ? 'on' : '', 'aria-selected': String(st.tab === 'detail'), onclick: () => { st.tab = 'detail'; st.inspKey = ''; renderInspector() } }, tr('รายละเอียด')))
}

function sessionPanel (sess, tab) {
  if (tab === 'chat') return [...sessionHeader(sess), h('div', { class: 'chatFrame' }, sessionTabs(), chatPanel(sess))]
  const ws = sess.dispatchVia && st.snap.workspaces.find(w => w.name === sess.dispatchVia)
  const usage = sess.usage || {}
  let tokens = tr('ไม่ได้นับ')
  if (usage.source !== 'notCounted') tokens = num(usage.inputTokens) + tr(' เข้า · ') + num(usage.outputTokens) + tr(' ออก · cache ') + num(usage.cachedTokens) + (usage.complete ? '' : tr(' (กำลังนับ… ยังไม่ครบ)'))
  const tl = (st.detail && st.detail.session.sessionId === sess.sessionId) ? st.detail.timeline : null
  const reach = sess.messageVia === 'agcmd'
    ? tr('ได้ — ผ่าน agcmd workspace ') + sess.dispatchVia + (ws ? ' (tmux ' + ws.tmuxSession + ')' : '')
    : sess.messageVia === 'relay' ? tr('ได้ — ผ่าน relay (tmux aiagent-') + sess.sessionId.slice(0, 8) + tr(' ที่รัน claude attach)') : tr('ไม่ได้ — ') + (sess.messageBlocked || tr('ไม่มีช่องทางที่ปลอดภัย'))
  return [
    ...sessionHeader(sess),
    sessionTabs(),
    sess.title ? h('p', null, sess.title) : null,
    h('dl', { class: 'kv' },
      h('dt', null, 'Session'), h('dd', { class: 'mono', title: sess.sessionId }, shortId(sess.sessionId)),
      h('dt', null, tr('เจ้าของ')), h('dd', null, sess.owner, sessPreviewRoom(sess) ? [' ', h('button', { type: 'button', class: 'ghost', title: tr('หน้าที่ agent ของห้องกำลังสร้าง'), onclick: () => openPreview(sessPreviewRoom(sess)) }, tr('🖥 พรีวิว'))] : null),
      h('dt', null, tr('โฟลเดอร์')), h('dd', { class: 'mono' }, sess.cwd || '—'),
      h('dt', null, 'Branch'), h('dd', { class: 'mono' }, sess.gitBranch || '—'),
      h('dt', null, tr('เริ่ม')), h('dd', null, sess.startedAt ? fmtTime(sess.startedAt) + ' · ' + ago(sess.startedAt) : '—'),
      h('dt', null, tr('เหตุการณ์ล่าสุด')), h('dd', null, ago(sess.lastEventAt)),
      h('dt', null, tr('หลักฐาน')), h('dd', null, (evidenceLabel[sess.liveEvidence] || sess.liveEvidence) + (sess.pid ? ' · pid ' + sess.pid : '')),
      h('dt', null, tr('ส่งข้อความจากเว็บ')), h('dd', null, reach),
      h('dt', null, tr('ดูในเครื่อง')), h('dd', null, localCmd(sess)),
      h('dt', null, 'Tokens'), h('dd', null, tokens),
      h('dt', null, tr('เครื่องมือล่าสุด')), h('dd', null, sess.lastTool || '—')
    ),
    sess.live && sess.stopVia
      ? h('button', { type: 'button', class: 'warn', onclick: () => doStop(sess) }, sess.stopVia === 'claudeStop' ? tr('หยุด agent (claude stop)') : tr('หยุด agent (agcmd restart)'))
      : (sess.live && sess.stopBlocked ? h('p', { class: 'note' }, tr('หยุดจากเว็บไม่ได้: ') + sess.stopBlocked) : null),
    sess.prs && sess.prs.length ? h('div', { class: 'btns' }, sess.prs.map(pr => h('a', { href: pr.url, target: '_blank', rel: 'noopener noreferrer', class: 'pill' }, 'PR #' + pr.number + ' · ' + pr.repository))) : null,
    subList(sess),
    tl ? h('div', { class: 'tl' }, h('h3', null, tr('ลำดับงานล่าสุด (ชื่อเครื่องมือเท่านั้น)')), tl.slice().reverse().slice(0, 14).map(t => h('div', null, h('span', null, fmtTime(t.at)), h('span', null, (activityLabel[t.activityType] || t.activityType) + ' · ' + t.tool)))) : h('p', { class: 'note' }, tr('กำลังโหลดลำดับงาน…'))
  ]
}

function localCmd (sess) {
  const cmd = sess.dispatchVia ? 'agcmd attach ' + sess.dispatchVia : sess.kind === 'bg' ? 'claude attach ' + sess.sessionId.slice(0, 8) : ''
  if (!cmd) return tr('เปิดอยู่ในเทอร์มินัล/VS Code ของผู้ใช้')
  return h('span', { class: 'cmd' }, h('code', null, cmd), h('button', { type: 'button', class: 'ghost copy', onclick: e => { copyText(cmd, e.currentTarget) } }, tr('คัดลอก')))
}

// secret: the text holds a token, so a failed copy never shows it on the button
function copyText (text, btn, secret) {
  const done = () => { btn.textContent = tr('คัดลอกแล้ว'); setTimeout(() => { btn.textContent = tr('คัดลอก') }, 1500) }
  const failed = () => { btn.textContent = secret ? tr('คัดลอกไม่ได้') : text }
  try {
    navigator.clipboard.writeText(text).then(done, failed)
  } catch (e) { failed() }
}

function chatPanel (sess) {
  const log = h('div', { class: 'chatLog', 'aria-live': 'polite' }, h('p', { class: 'note' }, tr('กำลังโหลดบทสนทนา…')))
  const parts = [log]
  // A held message the agent later refused is not "waiting": it is never
  // tried again, and saying "will type when free" hid that it never went.
  const all = pendingOf(sess.sessionId)
  const failed = all.filter(p => p.state === 'failed' || p.state === 'unknown')
  const waiting = all.filter(p => !failed.includes(p))
  if (waiting.length) {
    parts.push(h('div', { class: 'queued' },
      h('b', null, '⏳ ' + tr('รอส่ง ') + waiting.length + tr(' ข้อความ')),
      h('span', null, tr('agent กำลังทำงาน — จะพิมพ์ให้เองเมื่อว่าง')),
      h('button', { type: 'button', class: 'ghost', onclick: () => cancelPending(sess) }, tr('ยกเลิกที่รออยู่'))))
  }
  for (const p of failed) {
    parts.push(h('div', { class: 'queued bad' },
      h('b', null, '⚠ ' + tr('ส่งข้อความที่รอไว้ไม่สำเร็จ — จะไม่ลองใหม่เอง')),
      h('span', null, serverText(p.lastError || '')),
      h('button', { type: 'button', class: 'ghost', onclick: () => cancelPending(sess) }, tr('ล้างรายการ'))))
  }
  return h('div', { class: 'chat' }, parts)
}

// drawSending shows the message being sent at the foot of its session's chat.
// A send waits for the transcript to show it (up to ~8 s on the server), and
// the chat looked frozen meanwhile. updateChat rebuilds the log on every
// refresh, so it calls this again; the transcript's own copy replaces it once
// st.sending is cleared.
function drawSending () {
  const log = $('insp').querySelector('.chatLog')
  if (!log) return
  const old = log.querySelector('.msg.sending')
  if (old) old.remove()
  const s = st.sending
  if (!s || !st.sel || st.sel.id !== s.sid) return
  const lost = s.sent && s.delivery === 'unknown'
  log.append(h('div', { class: 'msg user sending' + (s.sent ? ' sent' : '') + (lost ? ' lost' : '') },
    h('div', { class: 'who2' }, tr('ผู้ใช้') + ' · ' + (lost ? '⚠ ' + tr('ยังไม่ยืนยันว่าถึง agent') : s.sent ? tr('ส่งแล้ว') : tr('กำลังส่ง…'))),
    mdBody(s.text, 'body')))
  log.scrollTop = log.scrollHeight
}

// settleSending drops an unconfirmed message once the transcript shows it.
function settleSending (id, msgs) {
  const s = st.sending
  if (!s || !s.sent || s.sid !== id) return
  const key = t => t.replace(/\s+/g, ' ').trim().slice(0, 60)
  if (!msgs.some(m => m.role === 'user' && Date.parse(m.at) >= s.at - 5000 && key(m.text) === key(s.text))) return
  st.sending = null
  drawSending()
}

async function updateChat (force) {
  const sel = st.sel
  if (!sel || sel.kind !== 'session') return
  const id = sel.id
  try {
    const res = await api('/sessions/' + encodeURIComponent(id) + '/messages?limit=60')
    if (!st.sel || st.sel.id !== id) return
    const msgs = res.messages || []
    settleSending(id, msgs)
    const sig = msgs.length + ':' + (msgs.length ? msgs[msgs.length - 1].at + msgs[msgs.length - 1].text.length : '')
    const log = $('insp').querySelector('.chatLog')
    if (!log || (!force && st.chatSig === sig)) return
    st.chatSig = sig
    const nearBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 80
    log.replaceChildren(...(msgs.length ? msgs.map(m => h('div', { class: 'msg ' + m.role },
      h('div', { class: 'who2' }, (m.role === 'user' ? tr('ผู้ใช้') : 'agent') + ' · ' + fmtTime(m.at)),
      mdBody(m.text, 'body'), msgImages(m.text))) : [h('p', { class: 'note' }, tr('ยังไม่มีข้อความ'))]))
    if (force || nearBottom) log.scrollTop = log.scrollHeight
    drawSending()
    const lastAgent = [...msgs].reverse().find(m => m.role === 'assistant')
    st.suggest = suggestFrom(lastAgent ? lastAgent.text : '')
    drawSuggest()
    // AI prediction like the CLI (cached per agent message on the server)
    const key = id + ':' + (lastAgent ? lastAgent.at : '')
    if (lastAgent && st.sugKey !== key) {
      st.sugKey = key
      api('/sessions/' + encodeURIComponent(id) + '/suggest').then(r => {
        if (!r || !r.suggestion || st.sugKey !== key) return
        st.suggest = [r.suggestion, ...(st.suggest || []).filter(x => x !== r.suggestion)].slice(0, 4)
        drawSuggest()
      }).catch(() => { st.sugKey = '' })
    }
  } catch (e) {
    const log = $('insp').querySelector('.chatLog')
    if (log && e.message !== 'auth') log.replaceChildren(h('p', { class: 'note' }, tr('โหลดบทสนทนาไม่ได้: ') + e.message))
  }
}

// attachments take a fixed-size stack in the row; the full list opens as a
// tray above the composer so the text box never shrinks
// A zip has no picture to show: it is drawn as a tile carrying its name.
function attTile (a, attrs) {
  if (a.kind === 'zip') return h('span', { class: 'zipTile', title: a.name || 'zip' }, h('b', null, 'ZIP'), h('small', null, a.name || ''))
  return h('img', Object.assign({ src: a.url }, attrs || {}))
}
// The lines a message carries for its attachments: the agent reads the paths.
function attachLines (files) {
  let pics = 0
  let zips = 0
  return files.map(f => f.kind === 'zip'
    ? tr('ไฟล์ zip แนบ ') + (++zips) + ': ' + f.path
    : tr('รูปแนบ ') + (++pics) + ': ' + f.path).join('\n')
}
const maxZipBytes = 50 * 1024 * 1024
function isZipFile (f) { return /^application\/(zip|x-zip-compressed)$/.test(f.type || '') || /\.zip$/i.test(f.name || '') }
function isZipPath (p) { return /\.zip$/i.test(p || '') }
function drawThumbs (thumbs) {
  // Two composers share one attachment list — the page footer and the monitor
  // dialog — so the tray has to be redrawn into whichever one is on screen.
  st.thumbsEl = thumbs
  const list = st.attach || []
  if (!list.length) { st.trayOpen = false; return thumbs.replaceChildren() }
  const stack = h('button', { type: 'button', class: 'attStack', 'aria-expanded': String(!!st.trayOpen), title: tr('ดู/จัดการรูปแนบ'), onclick: () => { st.trayOpen = !st.trayOpen; drawThumbs(thumbs) } },
    ...list.slice(-3).map(a => attTile(a, { alt: '' })), h('b', null, String(list.length)))
  const tray = st.trayOpen ? h('div', { class: 'attTray', role: 'dialog', 'aria-label': tr('รูปแนบ') },
    h('div', { class: 'trayHead' }, h('b', null, tr('รูปแนบ ') + list.length + '/10'), h('span', null, tr('คลิกรูปเพื่อดูใหญ่')),
      list.length < 10 ? h('button', { type: 'button', class: 'ghost', onclick: () => $('askFile').click() }, tr('+ เพิ่มรูป')) : null,
      h('button', { type: 'button', class: 'ghost', 'aria-label': tr('ปิด'), onclick: () => { st.trayOpen = false; drawThumbs(thumbs) } }, '✕')),
    h('div', { class: 'trayGrid' }, list.map((a, i) => h('div', { class: 'thumb' },
      attTile(a, { alt: tr('รูปแนบ ') + (i + 1), title: tr('คลิกเพื่อดูภาพใหญ่'), onclick: () => openLightbox(a.url) }),
      h('button', { type: 'button', 'aria-label': tr('ลบรูป ') + (i + 1), onclick: () => { if (a.url) URL.revokeObjectURL(a.url); st.attach.splice(i, 1); drawThumbs(thumbs); saveDraft() } }, '×'))))) : null
  thumbs.replaceChildren(stack, tray || '')
}
document.addEventListener('click', e => {
  if (st.trayOpen && e.target.isConnected && !e.target.closest('.thumbs') && !e.target.closest('#lightbox') && !e.target.closest('.clip')) { st.trayOpen = false; drawThumbs(st.thumbsEl || $('askThumbs')) }
})
// closing the monitor hands the attachments back to the composer in the footer
$('screenPop').addEventListener('close', () => { $('screenPopFoot').dataset.sig = ''; drawThumbs($('askThumbs')) })

// A photo off an iPad is routinely over the 3.5MB the service accepts, so the
// browser re-encodes it first. createImageBitmap is the direct way and is what
// this used to assume — but Safari has shipped versions that reject a Blob
// there, and on those every photo from the library failed to send. An <img>
// decodes everywhere, so it is the fallback.
async function decodeImage (blob) {
  if (typeof createImageBitmap === 'function') {
    try { return { src: await createImageBitmap(blob), done: () => {} } } catch (e) { /* fall through */ }
  }
  const url = URL.createObjectURL(blob)
  const im = new Image()
  try {
    await new Promise((resolve, reject) => {
      im.onload = resolve
      im.onerror = () => reject(new Error(tr('เปิดไฟล์รูปไม่ได้')))
      im.src = url
    })
  } catch (e) {
    URL.revokeObjectURL(url)
    throw e
  }
  return { src: im, done: () => URL.revokeObjectURL(url) }
}

async function shrink (blob) {
  if (blob.size <= 3400000) return blob
  const { src, done } = await decodeImage(blob)
  try {
    const k = Math.sqrt(3000000 / blob.size)
    const c = document.createElement('canvas')
    c.width = Math.max(1, Math.round(src.width * k))
    c.height = Math.max(1, Math.round(src.height * k))
    c.getContext('2d').drawImage(src, 0, 0, c.width, c.height)
    // toBlob hands back null when the canvas could not be encoded, which on
    // iOS happens under memory pressure. Uploading null fails far from here.
    const out = await new Promise(resolve => c.toBlob(resolve, 'image/jpeg', 0.85))
    if (!out) throw new Error(tr('ย่อรูปไม่สำเร็จ — ลองเลือกรูปที่เล็กกว่านี้'))
    return out
  } finally { done() }
}

// raw: send the bytes as they are. A zip must not go through shrink(), which
// re-encodes a picture.
async function uploadFile (blob, raw) {
  const body = raw ? blob : await shrink(blob)
  const res = await fetch('/aiagent/api/uploads', { method: 'POST', headers: { ...authHeaders(), 'Content-Type': body.type || 'application/octet-stream' }, body, cache: 'no-store' })
  let j = null
  try { j = await res.json() } catch (e) { j = null }
  if (!res.ok) throw new Error((j && serverText(j.message)) || ('HTTP ' + res.status))
  return j.details.path
}

const uploadRef = /\/uploads\/([0-9]{8}-[0-9]{6}-[0-9a-f]{16}\.(?:png|jpg|gif|webp))/g
st.imgCache = {}
function msgImages (text) {
  const names = [...text.matchAll(uploadRef)].map(m => m[1])
  if (!names.length) return null
  const wrap = h('div', { class: 'msgImgs' })
  for (const n of names.slice(0, 10)) {
    const im = h('img', { alt: n, title: tr('คลิกเพื่อดูภาพใหญ่') })
    im.addEventListener('click', () => { if (im.src) openLightbox(im.src) })
    wrap.append(im)
    loadUpload(n).then(u => { if (u) im.src = u }, () => {})
  }
  return wrap
}
async function loadUpload (name) {
  if (st.imgCache[name]) return st.imgCache[name]
  const res = await fetch('/aiagent/api/uploads/' + encodeURIComponent(name), { headers: authHeaders() })
  if (!res.ok) return ''
  st.imgCache[name] = URL.createObjectURL(await res.blob())
  return st.imgCache[name]
}

async function cancelPending (sess) {
  if (!await confirmBox(tr('ยกเลิกข้อความที่รอส่ง'), tr('ข้อความที่เก็บไว้ให้ ') + sess.displayName + tr(' จะถูกทิ้ง ไม่ถูกส่งให้ agent'), '', tr('ทิ้งเลย'))) return
  await act('/sessions/' + encodeURIComponent(sess.sessionId) + '/pending/cancel', {}, r => tr('ยกเลิกแล้ว ') + (r.cancelled || 0) + tr(' ข้อความ'))
}

async function sendChat (sess, ta, thumbs) {
  let text = expandPastes(ta.value).trim()
  const files = st.attach || []
  if (!text && !files.length) return
  st.confirmedChat = st.confirmedChat || {}
  if (!st.confirmedChat[sess.sessionId]) {
    const how = sess.messageVia === 'relay' ? tr('บริการนี้จะเปิด claude attach ') + sess.sessionId.slice(0, 8) + tr(' ใน tmux ส่วนตัวแล้วพิมพ์ข้อความนี้ให้ (เหมือนคุณพิมพ์เอง)') : tr('ส่งผ่าน agcmd dispatch ') + sess.dispatchVia
    if (!await confirmBox(tr('ส่งข้อความถึง ') + sess.displayName, how + tr(' · ยืนยันครั้งเดียวต่อ agent ในแท็บนี้'), text + (files.length ? tr('\n\n+ รูปแนบ ') + files.length + tr(' รูป') : ''), tr('ส่ง'))) return
    st.confirmedChat[sess.sessionId] = true
  }
  if (files.some(f => f.busy || !f.path)) { toast(tr('รูปยังอัปโหลดไม่เสร็จ — รออีกครู่แล้วกดส่งอีกที'), true); return }
  if (files.length) {
    text = (text || tr('ดูไฟล์ที่แนบ')) + '\n\n' + attachLines(files)
  }
  const withPics = files.length ? tr(' พร้อมรูป ') + files.length + tr(' รูป') : ''
  const mine = st.sending = { sid: sess.sessionId, text, sent: false, at: Date.now() }
  drawSending()
  const ok = await act('/sessions/' + encodeURIComponent(sess.sessionId) + '/message', { message: text, sendId: draftSendId(ta, sess.sessionId + '\n' + text) }, r => r.queued
    ? sess.displayName + tr(' กำลังทำงานอยู่ — เก็บข้อความไว้ให้แล้ว จะส่งเองเมื่อว่าง') + withPics
    : (mine.delivery = r.delivery) === 'unknown'
      ? tr('พิมพ์ส่งให้ ') + sess.displayName + tr(' แล้ว') + withPics
      : r.delivery === 'held'
        ? sess.displayName + tr(' กำลังทำงานอยู่ — ข้อความอยู่ในคิวของ agent แล้ว จะอ่านเมื่อถึงคิว') + withPics
        : tr('ส่งถึง ') + sess.displayName + tr(' แล้ว (') + (r.via || '') + ')' + (r.delivery === 'acked' ? tr(' · ยืนยันจาก transcript') : '') + withPics)
  if (!ok && st.sending === mine) { st.sending = null; drawSending() }
  if (ok) {
    mine.sent = true
    drawSending()
    forgetSendId(ta)
    ta.value = ''
    clearPastes()
    clearDraft()
    for (const f of files) { if (f.url) URL.revokeObjectURL(f.url) }
    st.attach = []
    if (thumbs) drawThumbs(thumbs)
    growText()
    setTimeout(() => {
      // a message nobody saw arrive stays on screen, marked, until it shows up
      if (st.sending === mine && mine.delivery !== 'unknown') st.sending = null
      updateChat(true)
    }, 1500)
  }
  ta.focus()
}

function runPanel (run) {
  const waiting = run.status === 'WAITING_DECISION'
  const done = ['DONE', 'ABORTED', 'ARCHIVED'].includes(run.status)
  const answerInput = h('input', { maxlength: 4000, placeholder: tr('พิมพ์คำตอบ…'), 'aria-label': tr('คำตอบ') })
  return [
    h('button', { type: 'button', class: 'ghost', onclick: back }, tr('← ภาพรวม')),
    h('h3', { class: 'mono' }, run.id + ' · ' + run.workspace),
    h('h2', null, run.lost ? tr('ขาดการเชื่อมต่อ') : (runLabel[run.status] || run.status)),
    h('p', null, run.task),
    h('dl', { class: 'kv' },
      h('dt', null, 'tmux'), h('dd', null, run.tmuxSession + (run.tmuxAlive ? tr(' · เปิดอยู่') : tr(' · ไม่พบ session'))),
      h('dt', null, tr('สร้าง')), h('dd', null, fmtTime(run.createdAt) + ' · ' + ago(run.createdAt)),
      h('dt', null, tr('อัปเดต')), h('dd', null, ago(run.updatedAt)),
      h('dt', null, tr('ที่มาของสถานะ')), h('dd', null, tr('agent/ผู้ใช้รายงานผ่าน agcmd (ไม่ได้วัดจาก process)'))
    ),
    waiting && run.decision ? h('div', { class: 'card warn' },
      h('b', null, run.decision.question),
      h('div', { class: 'btns' }, (run.decision.options || []).map(o => h('button', { type: 'button', onclick: () => doAnswer(run, o) }, o))),
      h('div', { class: 'answer' }, answerInput, h('button', { type: 'button', class: 'pri', onclick: () => doAnswer(run, answerInput.value) }, tr('ตอบ')))
    ) : null,
    done ? null : h('div', { class: 'btns' },
      h('button', { type: 'button', class: 'pri', onclick: () => doRunStatus(run, 'approve') }, tr('อนุมัติ (ตั้งเป็น READY)')),
      h('button', { type: 'button', class: 'warn', onclick: () => doRunStatus(run, 'abort') }, tr('ยกเลิกงาน'))
    ),
    h('p', { class: 'note' }, tr('อนุมัติ/ยกเลิก เปลี่ยนสถานะใน agcmd เท่านั้น — ไม่ merge, ไม่ deploy และไม่ปิด process')),
    h('h3', null, tr('บันทึกของงาน')),
    h('div', { class: 'log mono' }, st.runLog ? (st.runLog.join('\n') || tr('(ว่าง)')) : tr('กำลังโหลด…'))
  ]
}

function overviewPanel () {
  const snap = st.snap
  const needs = []
  for (const sess of snap.sessions) {
    if (sess.status === 'needsInput') needs.push(h('div', { class: 'card warn', tabindex: 0, onclick: () => { st.tab = 'chat'; flyTo('session', sess.sessionId) } }, h('b', null, sess.displayName + tr(' รอคุณตอบ')), h('small', null, (sess.messageVia ? tr('ตอบจากเว็บได้ · ') : tr('ดูได้อย่างเดียว · ')) + ago(sess.lastEventAt))))
  }
  for (const run of snap.runs) {
    if (run.status === 'WAITING_DECISION') needs.push(h('div', { class: 'card warn', tabindex: 0, onclick: () => select('run', run.id) }, h('b', null, (run.decision && run.decision.question) || run.task.slice(0, 120)), h('small', null, run.id + ' · ' + run.workspace + ' · ' + ago(run.updatedAt))))
    else if (run.lost) needs.push(h('div', { class: 'card bad', tabindex: 0, onclick: () => select('run', run.id) }, h('b', null, run.id + tr(' ขาดการเชื่อมต่อ')), h('small', null, tr('สถานะ RUNNING แต่ไม่พบ tmux ') + run.tmuxSession)))
  }
  const quotas = snap.quotas.map(q => {
    const fill = h('div', { class: 'fill' })
    if (q.confidence === 'actual') {
      fill.style.width = Math.max(0, Math.min(100, q.remainingPercent)) + '%'
      fill.style.background = q.mode === 'normal' ? 'var(--brand)' : q.mode === 'reserve' ? 'var(--rose)' : 'var(--amber)'
    } else {
      fill.style.width = '0'
    }
    const right = q.confidence === 'actual' ? tr('ใช้ไป ') + Math.round(q.usedPercent) + '%' : (q.confidence === 'stale' ? tr('รีเซ็ตแล้ว') : tr('ไม่ทราบ'))
    const note = q.confidence === 'actual'
      ? tr('รอบ') + windowName(q.windowMinutes) + (q.resetsAt ? tr(' · รีเซ็ต ') + new Date(q.resetsAt).toLocaleString('th-TH', { dateStyle: 'short', timeStyle: 'short' }) : '') + tr(' · อ่านเมื่อ ') + ago(q.observedAt)
      : q.source
    return h('div', { class: 'quota' }, h('span', null, (providerName[q.providerId] || q.providerId) + (q.planType ? ' · ' + q.planType : '')), h('b', null, right), h('div', { class: 'track' }, fill), h('small', { class: 'note' }, note))
  })
  return [
    ...pmOverview(),
    h('h2', null, tr('ภาพรวมวันนี้')),
    h('h3', null, tr('ต้องให้คุณช่วย (') + needs.length + ')'),
    needs.length ? needs : h('p', { class: 'note' }, tr('ไม่มีงานที่รอคุณ')),
    reviewList(false),
    h('h3', null, tr('พลังงาน AI')),
    quotas,
    quotaAdvice(),
    isUser() ? null : policySlot(),
    h('p', { class: 'note' }, tr('ตัวเลข quota มาจากผู้ให้บริการเท่านั้น ถ้าไม่มีข้อมูลจะแสดง “ไม่ทราบ” ไม่ประมาณเอง')),
    h('h3', null, tr('วิธีใช้')),
    h('p', { class: 'note' }, tr('คลิกตัวละครหรือแถวในมุมมองรายการเพื่อดูรายละเอียด · ช่องด้านล่างใช้สั่งงานไปยัง agent ใน workspace ของ agcmd'))
  ]
}

// quotaAdvice: how to get a Claude reading, or which agents to move to a
// cheaper model when the plan window is running low
function quotaAdvice () {
  const qs = st.snap.quotas.filter(q => q.providerId === 'anthropic')
  if (qs.every(q => q.confidence === 'unknown')) {
    return h('div', { class: 'advice' }, h('b', null, tr('เปิดการอ่าน % ของ Claude')),
      h('p', null, tr('Claude Code ส่งตัวเลข % ให้เฉพาะ status line — ตั้งครั้งเดียวใน ~/.claude/settings.json:')),
      h('code', null, '"statusLine": {"type": "command", "command": "aiagent statusline"}'),
      h('p', { class: 'note' }, tr('ตัวเลขอัปเดตเมื่อมี Claude session ที่เปิดหน้าจออยู่อย่างน้อยหนึ่งตัว')))
  }
  const worst = qs.filter(q => q.confidence === 'actual').sort((a, b) => b.usedPercent - a.usedPercent)[0]
  if (!worst || worst.usedPercent < 60) return null
  if (isUser()) return null
  const heavy = st.snap.sessions.filter(x => x.live && x.runtimeType === 'claudeCode' && /opus|fable/i.test(x.model || '') && x.messageVia)
  return h('div', { class: 'advice warn' }, h('b', null, tr('Claude ใช้ไป ') + Math.round(worst.usedPercent) + '% (' + windowName(worst.windowMinutes) + ')'),
    h('p', null, heavy.length ? tr('แนะนำลด agent ที่ใช้ ') + modelName('opus') + tr(' เป็น Sonnet เพื่อยืดโควตา:') : tr('ไม่มี agent ที่สั่งได้ซึ่งใช้ model ใหญ่อยู่')),
    ...heavy.map(x => h('button', { type: 'button', class: 'ghost', onclick: () => switchModel(x, 'sonnet') }, x.displayName + ' → Sonnet')))
}

function modelName (id) {
  if (!id) return tr('ไม่ทราบ')
  const m = /^claude-([a-z]+)-(\d+)(?:-(\d+))?/.exec(id)
  if (m) return m[1][0].toUpperCase() + m[1].slice(1) + ' ' + m[2] + (m[3] && m[3].length < 3 ? '.' + m[3] : '')
  if (/^[a-z]+$/.test(id)) return id[0].toUpperCase() + id.slice(1)
  return id
}

async function switchModel (sess, alias) {
  const ok = await confirmBox(tr('เปลี่ยน model'), tr('จะพิมพ์ /model ') + alias + tr(' เข้า ') + sess.displayName + ' (' + (sess.messageVia === 'agcmd' ? 'agcmd ' + sess.dispatchVia : 'relay') + tr(') — มีผลกับข้อความถัดไปของ agent ตัวนี้'), tr('ปัจจุบัน: ') + modelName(sess.model) + ' → ' + modelName(alias), tr('เปลี่ยนเลย'))
  if (!ok) return
  await act('/sessions/' + encodeURIComponent(sess.sessionId) + '/model', { model: alias }, () => tr('เปลี่ยน ') + sess.displayName + tr(' เป็น ') + modelName(alias) + tr(' แล้ว'))
}

async function loadModels () {
  if (st.models && Date.now() - st.models.at < 60000) return st.models
  try { st.models = Object.assign({ at: Date.now() }, await api('/models')) } catch (e) { st.models = st.models || { claude: [], codex: [], at: 0 } }
  return st.models
}

function modelRow (sess) {
  const row = h('div', { class: 'modelRow' }, h('span', { class: 'modelChip', title: sess.model || '' }, '🧠 ' + modelName(sess.model)))
  if (isUser()) { row.append(h('small', { class: 'note' }, tr('admin เป็นผู้กำหนด model'))); return row }
  const codex = sess.runtimeType === 'codexCli'
  const can = sess.live && !codex && sess.messageVia
  loadModels().then(ms => {
    if (codex) {
      row.append(h('select', {
        'aria-label': tr('model เริ่มต้นของ Codex'),
        onchange: async e => {
          const v = e.target.value
          e.target.value = ''
          if (!v || !await confirmBox(tr('เปลี่ยน model เริ่มต้นของ Codex'), tr('Codex ที่รันอยู่เปลี่ยนจากภายนอกไม่ได้ — ค่านี้มีผลกับ session ใหม่ (~/.codex/config.toml)'), tr('เป็น ') + v, tr('เปลี่ยน'))) return
          await act('/codex/model', { model: v }, () => tr('Codex session ใหม่จะใช้ ') + v)
          st.models = null
        }
      }, h('option', { value: '' }, tr('model เริ่มต้น: ') + (ms.codexDefault || '—')), ...(ms.codex || []).filter(m => m.id !== ms.codexDefault).map(m => h('option', { value: m.id }, m.label))))
    } else if (can) {
      const cur = (sess.model || '').toLowerCase()
      row.append(h('select', { 'aria-label': tr('เปลี่ยน model'), onchange: e => { const v = e.target.value; e.target.value = ''; if (v) switchModel(sess, v) } },
        h('option', { value: '' }, tr('เปลี่ยน model…')),
        ...(ms.claude || []).filter(m => m.id !== cur).map(m => h('option', { value: m.id }, m.label))))
    } else {
      row.append(h('small', { class: 'note' }, tr('เปลี่ยนได้เฉพาะ agent ที่รับข้อความจากเว็บ')))
    }
  })
  return row
}

// ----- auto downgrade policy (overview panel) -----
function policySlot () {
  const slot = h('div', { class: 'policySlot' })
  policyBox().then(el => slot.replaceChildren(el))
  return slot
}
async function policyBox () {
  const boxEl = h('div', { class: 'advice policy' }, h('b', null, tr('⚙️ ลด model อัตโนมัติเมื่อ quota เกิน')))
  let p, ms, roots
  try { const r = await api('/policy'); p = r.policy; roots = r.trustedRoots || []; ms = await loadModels() } catch (e) { return boxEl }
  const on = h('input', { type: 'checkbox' })
  on.checked = !!p.enabled
  const th = h('input', { type: 'number', min: 30, max: 99, step: 1, value: p.thresholdPct })
  const cm = h('select', null, ...(ms.claude || []).map(m => h('option', { value: m.id }, m.label)))
  cm.value = p.claudeModel
  const xm = h('select', null, ...(ms.codex || []).map(m => h('option', { value: m.id }, m.label)))
  if (![...xm.options].some(o => o.value === p.codexModel)) xm.append(h('option', { value: p.codexModel }, p.codexModel))
  xm.value = p.codexModel
  const save = async () => {
    try {
      await api('/policy', { method: 'POST', body: { enabled: on.checked, thresholdPct: Number(th.value), claudeModel: cm.value, codexModel: xm.value } })
      toast(on.checked ? tr('เปิดลด model อัตโนมัติที่ ') + th.value + '%' : tr('ปิดลด model อัตโนมัติแล้ว (จะคืน model เดิมให้)'))
    } catch (e) { toast(tr('บันทึกไม่ได้: ') + e.message, true) }
  }
  boxEl.append(
    h('label', { class: 'polRow' }, on, h('span', null, tr('เปิดใช้'))),
    h('label', { class: 'polRow' }, h('span', null, tr('เมื่อใช้ไปเกิน')), th, h('span', null, '%')),
    h('label', { class: 'polRow' }, h('span', null, 'Claude →'), cm),
    h('label', { class: 'polRow' }, h('span', null, tr('Codex (session ใหม่) →')), xm),
    h('p', { class: 'note' }, tr('ตรวจทุก 1 นาที · คืน model เดิมเมื่อใช้ลดลงต่ำกว่าเกณฑ์ 15 จุด หรือรอบรีเซ็ต · บันทึก audit ทุกครั้ง')),
    p.lastAction ? h('p', { class: 'note' }, tr('ล่าสุด: ') + p.lastAction + ' · ' + ago(p.lastActionAt)) : '',
    h('button', { type: 'button', class: 'pri', onclick: save }, tr('บันทึก')))
  const io = h('input', { type: 'checkbox' })
  io.checked = !!p.idleIdeas
  const im = h('input', { type: 'number', min: 10, max: 1440, step: 5, value: p.idleMinutes || 30 })
  const iq = h('input', { type: 'number', min: 5, max: 95, step: 5, value: p.idleMaxQuota || 50 })
  const wsList = (st.snap.workspaces || []).map(w => w.name)
  const iw = h('select', null, ...(wsList.length ? wsList : [p.idleTarget].filter(Boolean)).map(n => h('option', { value: n }, n)))
  iw.value = p.idleTarget || ''
  const saveIdle = async () => {
    try {
      await api('/policy/idle', { method: 'POST', body: { enabled: io.checked, minutes: Number(im.value), maxQuota: Number(iq.value), target: iw.value } })
      toast(io.checked ? tr('เปิดงานช่วงว่าง: ว่างเกิน ') + im.value + tr(' นาที และ quota ต่ำกว่า ') + iq.value + '%' : tr('ปิดงานช่วงว่างแล้ว'))
    } catch (e) { toast(tr('บันทึกไม่ได้: ') + e.message, true) }
  }
  boxEl.append(h('hr'), h('b', null, tr('💡 ใช้ช่วงว่างคิดงาน (เสนอก่อน รออนุมัติ)')),
    h('label', { class: 'polRow' }, io, h('span', null, tr('เปิดใช้'))),
    h('label', { class: 'polRow' }, h('span', null, tr('ว่างเกิน')), im, h('span', null, tr('นาที'))),
    h('label', { class: 'polRow' }, h('span', null, tr('และ quota ต่ำกว่า')), iq, h('span', null, '%')),
    h('label', { class: 'polRow' }, h('span', null, tr('ให้ agent ใน workspace')), iw),
    h('p', { class: 'note' }, tr('agent จะคิดไอเดียเกม asset และจุดเสี่ยง/ช้า (ปรึกษา Codex แบบอ่านอย่างเดียวได้) แล้วส่งเป็นหน้าตรวจงานในห้องทดสอบ — ห้ามแก้โค้ดจนกว่าคุณอนุมัติ · สูงสุด 1 ครั้งต่อ 6 ชม. และไม่ส่งซ้ำถ้ายังมีข้อเสนอรอตรวจ')),
    p.lastIdeaAt ? h('p', { class: 'note' }, tr('ส่งล่าสุด ') + ago(p.lastIdeaAt)) : '',
    h('button', { type: 'button', class: 'pri', onclick: saveIdle }, tr('บันทึก')))
  // read-only: roots are changed with `aiagent trust-roots` on the machine
  boxEl.append(h('hr'), h('b', null, tr('📂 โฟลเดอร์ที่เชื่อถืออัตโนมัติ (trusted roots)')),
    roots.length ? h('ul', null, ...roots.map(r => h('li', null, h('code', null, r)))) : h('p', { class: 'note' }, tr('ยังไม่ได้ตั้ง — ไม่มีโฟลเดอร์ไหนถูกเชื่อถืออัตโนมัติ')),
    h('p', { class: 'note' }, tr('แก้ได้ที่เครื่องด้วย aiagent trust-roots add|remove|list')))
  return boxEl
}

// ----- composer suggestions (Tab to accept), from the agent's last message -----
function suggestFrom (text) {
  const out = []
  const add = v => { v = v.trim().replace(/[.。]$/, ''); if (v && v.length <= 120 && !out.includes(v)) out.push(v) }
  const t = text || ''
  const ask0 = (t.match(/needs input:\s*([^\n]+)/) || [])[1] || (t.trim().split('\n').filter(Boolean).pop() || '')
  for (const m of ask0.matchAll(/\(([ก-ฮa-c])\)\s*([^(\n]{2,80})/g)) add('(' + m[1] + ') ' + m[2].replace(/[\s,]*(หรือ|และ|or|and)?[\s,]*$/i, ''))
  for (const m of t.matchAll(/`([^`\n]{3,80})`/g)) {
    const c = m[1].trim()
    if (/^(!|approve |merge |deploy |ทำต่อ|ตกลง|ok\b|yes\b|ใช่)/i.test(c)) add(c)
  }
  // the question the agent is waiting on: prefer its own example answer
  const ask = (t.match(/needs input:\s*([^\n]+)/) || [])[1] || (t.trim().split('\n').filter(Boolean).pop() || '')
  for (const m of ask.matchAll(/\((?:เช่น|ตัวอย่าง|e\.g\.,?)\s*([^)]{2,80})\)/gi)) add(tr('ใช่ ') + m[1])
  if (/ไหม|หรือไม่|ใช่ไหม|\?\s*$/.test(ask)) { add(tr('ใช่')); add(tr('ไม่')) }
  if (!out.length && t) add(tr('ทำต่อได้เลย'))
  if (t) add(tr('สรุปสถานะล่าสุดให้หน่อย'))
  return out.slice(0, 4)
}
function drawSuggest () {
  const box = $('suggest')
  const list = composerSession() ? (st.suggest || []) : []
  box.hidden = !list.length
  box.replaceChildren(...list.map((v, i) => h('button', { type: 'button', class: 'sug' + (i === 0 ? ' first' : ''), title: i === 0 ? tr('กด Tab เพื่อใช้') : tr('คลิกเพื่อใช้'), onclick: () => useSuggest(v) }, i === 0 ? h('kbd', null, 'Tab') : '', v)))
}
function useSuggest (v) {
  const ta = $('askText')
  ta.value = v
  ta.focus()
  ta.setSelectionRange(v.length, v.length)
  growText()
}

// ----- reviews: HTML pages agents file for the operator to check -----
// Messages the service is holding for this agent because it was busy.
function pendingOf (sid) {
  return ((st.snap && st.snap.pending) || []).filter(p => p.sessionId === sid)
}

function openReviewsOf (sid) {
  return (st.reviews || []).filter(r => r.status === 'open' && r.sessionId && sid.startsWith(r.sessionId.slice(0, 8)) && r.sessionId.startsWith(sid.slice(0, 8)))
}
function openPages () {
  return (st.reviews || []).filter(r => r.status === 'open')
}
function sessOfReview (r) {
  return r.sessionId ? st.snap.sessions.find(x => x.sessionId.startsWith(r.sessionId) || r.sessionId.startsWith(x.sessionId)) : null
}
function reviewList (inPop) {
  const all = st.reviews || []
  const open = all.filter(r => r.status === 'open')
  const recent = inPop ? all.filter(r => r.status !== 'open').slice(0, 5) : []
  const row = r => {
    const sess = sessOfReview(r)
    return h('button', { type: 'button', class: 'revItem ' + r.status, onclick: () => { if ($('screenPop').open) $('screenPop').close(); openReview(r.id) } },
      h('b', null, (r.status === 'open' ? '🧪 ' : r.status === 'approved' ? '✅ ' : '✏️ ') + r.title),
      h('small', null, (r.owner ? tr('จากห้อง ') + r.owner + ' · ' : '') + (sess ? sess.displayName : r.runtime === 'codexCli' ? 'Codex' : tr('ไม่ระบุ session')) + ' · ' + ago(r.createdAt)),
      r.summary ? h('p', null, r.summary) : null)
  }
  return h('div', { class: 'revList' },
    h('h3', null, tr('งานรอตรวจ (') + open.length + ')' + (inPop ? '' : tr(' · ห้องทดสอบ'))),
    open.length ? open.map(row) : h('p', { class: 'note' }, tr('ยังไม่มีหน้าตรวจงาน — ให้ agent สร้างหน้า HTML สรุปงานแล้วรัน: aiagent review --title "…" page.html')),
    recent.length ? h('h3', null, tr('ตรวจแล้วล่าสุด')) : null, recent.map(row))
}

// After a review's message went to its agent: close the review popup and open
// that session on its chat tab, so the owner sees the message that went in.
// A stopped session that was just resumed may not be in the snapshot yet, and
// the transcript can lag the send, so it looks again a few times.
// After a review is answered: read the list again now, and once more a few
// seconds later (a busy agent gets the answer from the outbox, and a room's
// list is read through its own instance), so the answered one leaves the list
// without a manual refresh.
function refreshReviews () {
  st.revAt = 0
  poll()
  setTimeout(() => { st.revAt = 0; poll() }, 6000)
}
function sentToSession (r) {
  if ($('reviewDlg').open) $('reviewDlg').close()
  let tries = 0
  const look = () => {
    const sess = sessOfReview(r)
    if (!sess) {
      if (++tries < 8) setTimeout(look, 2000)
      return
    }
    st.tab = 'chat'
    select('session', sess.sessionId)
    for (const ms of [1500, 5000]) setTimeout(() => { if (st.sel && st.sel.id === sess.sessionId) updateChat(true) }, ms)
  }
  look()
}

async function openReview (id) {
  const r = (st.reviews || []).find(x => x.id === id)
  if (!r) return
  const sess = sessOfReview(r)
  const open = r.status === 'open'
  // The service sends to the session stored on the review: a running one
  // through its own channel, a stopped Claude Code one by resuming it.
  const resumable = r.runtime === 'claudeCode' && !!r.sessionId
  const canSend = open && !!r.sessionId && (sess && sess.live ? !!sess.messageVia : resumable)
  // answered, typed, but never seen in the transcript: it may not have arrived
  const unconfirmed = !open && !!r.sentAt && r.delivery === 'unknown' && r.sentVia !== 'queued' && !!r.sessionId
  const cantSendWhy = unconfirmed
    ? tr('ผลตรวจนี้พิมพ์ให้ agent ไปแล้วแต่ยังไม่ยืนยันว่าถึง — เปิด session ดูก่อน ถ้าไม่ถึงกดส่งซ้ำ')
    : !open
    ? tr('งานนี้ตรวจไปแล้ว — ส่งซ้ำไม่ได้ (คัดลอกข้อความได้)')
    : !r.sessionId
      ? tr('หน้านี้ไม่ได้บอกว่ามาจาก session ไหน — กดคัดลอกแล้ววางใน Claude / Codex เอง')
      : sess && sess.live
        ? tr('ส่งตรงไม่ได้: ') + (sess.messageBlocked || tr('session นี้รับข้อความจากเว็บไม่ได้')) + tr(' — กดคัดลอกแล้ววางเอง')
        : tr('session นี้ไม่ได้รันอยู่และเปิดต่อจากเว็บไม่ได้ — กดคัดลอกแล้ววางเอง')
  // The result of a send, shown in the dialog itself: the page banner sits
  // under this modal, so a toast alone was invisible and the button looked dead.
  const sendState = h('p', { class: 'revSendState', role: 'status', 'aria-live': 'polite', hidden: true })
  const showSend = (text, kind) => {
    sendState.hidden = false
    sendState.className = 'revSendState ' + (kind || '')
    sendState.textContent = text
  }
  const sentText = res => res.queued
    ? tr('⏳ ') + (sess ? sess.displayName : tr('agent')) + tr(' กำลังทำงานอยู่ — เก็บข้อความไว้แล้ว จะพิมพ์ให้อัตโนมัติเมื่อว่าง')
    : (res.delivery === 'unknown' ? tr('⚠ พิมพ์ให้ ') : tr('✅ ส่งให้ ')) + (sess ? sess.displayName : r.sessionId.slice(0, 8)) + tr(' แล้ว (') + res.via + ')' +
      (res.delivery === 'acked' ? tr(' · agent ได้รับแล้ว') : res.delivery === 'held' ? tr(' · อยู่ในคิวของ agent แล้ว') : tr(' · ยังไม่ยืนยันว่าถึง agent — เปิด session ดูก่อนส่งซ้ำ'))
  const failText = e => tr('❌ ส่งไม่สำเร็จ: ') + e.message + tr(' — งานยังไม่ถูกปิด กดส่งใหม่ หรือคัดลอกแล้ววางเองได้')
  const sendingText = tr('กำลังส่งให้ agent… (ถ้าต้องเปิด session ต่ออาจใช้ครึ่งนาที)')
  let busy = false
  let items = null
  let confirmed = false
  let ready = false
  let timer = 0
  let seq = 0
  $('reviewTitle').textContent = tr('🧪 ตรวจงาน · ') + r.title
  $('reviewFrame').removeAttribute('src')
  reviewTabs(r)

  const note = h('textarea', { rows: 2 })
  // Pictures for the note: a screenshot with the mistake circled says more
  // than a sentence. They are uploaded like a composer picture and their
  // paths ride in the note, which is what the service sends to the agent.
  const pics = []
  const picsBusy = () => pics.some(p => p.busy)
  const noteText = () => {
    const done = pics.filter(p => p.path)
    return note.value + (done.length ? (note.value.trim() ? '\n\n' : '') + attachLines(done) : '')
  }
  const picGrid = h('div', { class: 'trayGrid' })
  const picInput = h('input', { type: 'file', accept: 'image/png,image/jpeg,image/gif,image/webp', multiple: '', hidden: '' })
  const picBtn = h('button', { type: 'button', class: 'ghost', disabled: !open, onclick: () => picInput.click() }, tr('📎 แนบภาพ'))
  const drawPics = () => {
    picGrid.replaceChildren(...pics.map((p, i) => h('div', { class: 'thumb' + (p.busy ? ' busy' : '') },
      h('img', { src: p.url, alt: tr('รูปแนบ ') + (i + 1), title: tr('คลิกเพื่อดูภาพใหญ่'), onclick: () => openLightbox(p.url) }),
      h('button', { type: 'button', 'aria-label': tr('ลบรูป ') + (i + 1), onclick: () => { URL.revokeObjectURL(p.url); pics.splice(pics.indexOf(p), 1); drawPics(); picsChanged() } }, '×'))))
    picBtn.disabled = !open || pics.length >= 10
  }
  const addPics = async list => {
    let skipped = 0
    const fresh = []
    for (const f of list) {
      if (!f) continue
      if (!/^image\/(png|jpeg|gif|webp)$/.test(f.type) || pics.length + fresh.length >= 10) { skipped++; continue }
      fresh.push({ blob: f, url: URL.createObjectURL(f), path: '', busy: true })
    }
    if (skipped) toast(tr('แนบไม่ได้ ') + skipped + tr(' ไฟล์ — ช่องนี้รับเฉพาะ PNG, JPEG, GIF, WebP และไม่เกิน 10 รูป'), true)
    if (!fresh.length) return
    pics.push(...fresh)
    drawPics()
    paint()
    for (const p of fresh) {
      try {
        p.path = await uploadFile(p.blob, false)
      } catch (e) {
        const i = pics.indexOf(p)
        if (i >= 0) pics.splice(i, 1)
        URL.revokeObjectURL(p.url)
        toast(tr('อัปโหลดไม่สำเร็จ: ') + e.message, true)
      }
      p.busy = false
      drawPics()
      picsChanged()
    }
  }
  picInput.addEventListener('change', () => { addPics(picInput.files); picInput.value = '' })
  note.addEventListener('paste', e => {
    const files = [...(e.clipboardData ? e.clipboardData.items : [])].filter(i => i.kind === 'file').map(i => i.getAsFile())
    if (!files.length || !open) return
    e.preventDefault()
    addPics(files)
  })
  const picRow = h('div', { class: 'revPics' }, h('div', { class: 'revPicsHead' }, picBtn, picInput, h('span', { class: 'note' }, tr('วงจุดที่ผิดในภาพแล้วแนบ หรือวางภาพลงในช่องหมายเหตุได้'))), picGrid)
  const checks = h('ul', { class: 'revChecks' })
  const status = h('p', { class: 'note' }, tr('กำลังอ่านหน้าตรวจ…'))
  const queue = h('pre', { class: 'revQueue' }, tr('ยังไม่ได้ยืนยันผลตรวจ — ตรวจทุกข้อในหน้าแล้วกดยืนยัน'))
  const out = h('pre', { class: 'revReply', hidden: true })
  const confirmBtn = h('button', { type: 'button', class: 'pri', disabled: true, onclick: () => confirmItems() }, tr('ยืนยันผลตรวจ'))
  const sendBtn = h('button', { type: 'button', class: 'pri', disabled: true, onclick: () => finish(true) }, tr('ส่งให้ agent'))
  const copyBtn = h('button', { type: 'button', class: 'ghost', disabled: true, onclick: () => finish(false) }, tr('คัดลอกและปิดงาน'))
  const itemsBox = h('div', { class: 'revItems', hidden: true },
    h('p', { class: 'note' }, tr('ตรวจทีละข้อในหน้า (ผ่าน / ต้องแก้ไข) แล้วกดยืนยันผลตรวจ')),
    checks,
    confirmBtn,
    h('h3', null, tr('ข้อความรอส่ง')),
    queue,
    h('p', { class: 'note' }, tr('แก้ผลข้อไหน ข้อความรอส่งจะอัปเดตให้เอง')),
    h('div', { class: 'revBtns' }, sendBtn, copyBtn))
  const decideLegacy = async (decision, sendIt) => {
    if (decision === 'revise' && !noteText().trim()) { note.focus(); toast(tr('บอกสิ่งที่ต้องแก้ก่อน'), true); return }
    if (picsBusy()) { toast(tr('รูปยังอัปโหลดไม่เสร็จ — รออีกครู่แล้วกดส่งอีกที'), true); return }
    if (busy) return
    busy = true
    const sends = [...legacyBox.querySelectorAll('button.pri, button.warn')]
    sends.forEach(b => { b.disabled = true })
    if (sendIt) showSend(sendingText, 'busy')
    try {
      const res = await call('/decision', { decision, note: noteText(), send: sendIt })
      out.hidden = false
      out.textContent = res.reply
      if (!sendIt) copyText(res.reply, { textContent: '' })
      const msg = sendIt ? sentText(res) : tr('คัดลอกข้อความตอบกลับแล้ว — วางใน session ได้เลย')
      showSend(msg, 'ok')
      toast(msg)
      refreshReviews()
      if (sendIt) sentToSession(r)
    } catch (e) {
      showSend(failText(e), 'bad')
      toast(failText(e), true)
      busy = false
      sends.forEach(b => { b.disabled = !canSend })
    }
  }
  const legacyBox = h('div', { hidden: true },
    h('div', { class: 'revBtns' },
      h('b', null, tr('✅ ตกลง ปิดงาน')),
      h('button', { type: 'button', class: 'ghost', onclick: () => decideLegacy('approve', false) }, tr('คัดลอกข้อความ')),
      h('button', { type: 'button', class: 'pri', disabled: !canSend, onclick: () => decideLegacy('approve', true) }, tr('ส่งให้ agent'))),
    h('div', { class: 'revBtns' },
      h('b', null, tr('✏️ ขอแก้ไขเพิ่มเติม')),
      h('button', { type: 'button', class: 'ghost', onclick: () => decideLegacy('revise', false) }, tr('คัดลอกข้อความ')),
      h('button', { type: 'button', class: 'warn', disabled: !canSend, onclick: () => decideLegacy('revise', true) }, tr('ส่งให้ agent'))))

  const call = (path, body) => api('/reviews/' + encodeURIComponent(id) + path, { method: 'POST', body })
  const paint = () => {
    if (items === null) { status.hidden = false; itemsBox.hidden = true; legacyBox.hidden = true; return }
    status.hidden = true
    const list = items.length > 0
    itemsBox.hidden = !list
    legacyBox.hidden = list
    note.placeholder = list ? tr('หมายเหตุรวมถึงผู้ทำงาน (ไม่บังคับ)') : tr('หมายเหตุ / สิ่งที่ต้องแก้ (ถ้าขอแก้ต้องใส่)')
    if (!list) return
    checks.replaceChildren(...items.map((it, i) => h('li', { class: 'revCheck ' + (it.verdict || 'todo') },
      h('b', null, (i + 1) + '. ' + it.title),
      h('span', null, it.verdict === 'approve' ? '✅ ' + tr('ผ่าน') : it.verdict === 'revise' ? '✏️ ' + tr('ต้องแก้ไข') : tr('ยังไม่ตรวจ')),
      it.verdict === 'revise' && it.note ? h('small', null, it.note) : null)))
    const judged = items.every(it => it.verdict)
    confirmBtn.disabled = !judged || !open || confirmed
    confirmBtn.textContent = confirmed ? tr('ยืนยันผลตรวจแล้ว') : tr('ยืนยันผลตรวจ')
    sendBtn.disabled = !canSend || !ready || busy || picsBusy()
    copyBtn.disabled = !open || !ready || busy || picsBusy()
  }
  const composeNow = async () => {
    timer = 0
    const mine = ++seq
    const missing = items.findIndex(it => it.verdict === 'revise' && !(it.note || '').trim())
    if (missing >= 0) {
      ready = false
      queue.textContent = tr('ใส่สิ่งที่ต้องแก้ในข้อ ') + (missing + 1) + tr(' ก่อน')
      paint()
      return
    }
    try {
      const res = await call('/compose', { items, note: noteText() })
      if (mine !== seq) return
      ready = true
      queue.textContent = res.reply
    } catch (e) {
      if (mine !== seq) return
      ready = false
      queue.textContent = tr('ร่างข้อความไม่ได้: ') + e.message
    }
    paint()
  }
  const later = () => { clearTimeout(timer); timer = setTimeout(composeNow, 250) }
  // once confirmed, the message to send is below the list: bring the send
  // button into view instead of leaving the owner to scroll for it
  const confirmItems = async () => {
    confirmed = true
    await composeNow()
    sendBtn.scrollIntoView({ behavior: 'smooth', block: 'center' })
  }
  // The reply went out once but no transcript showed it: the service refuses
  // a second press (AIAGENT_REVIEW_UNCONFIRMED) and sends it again only when
  // the operator says so here, as a composer's "send again" does.
  const sendAgain = async (why) => {
    if (!await confirmBox(tr('ผลตรวจนี้ส่งไปแล้ว'), why, r.reply || '', tr('ส่งซ้ำ'))) {
      toast(tr('ไม่ได้ส่งซ้ำ — ข้อความเดิมส่งไปแล้ว'), true)
      return null
    }
    showSend(sendingText, 'busy')
    return call('/decision', { send: true, resend: true })
  }
  const sentAgain = res => {
    const msg = sentText(res)
    showSend(msg, 'ok')
    toast(msg)
    refreshReviews()
    sentToSession(r)
  }
  const finish = async (sendIt) => {
    if (!ready || busy) return
    busy = true
    paint()
    if (sendIt) showSend(sendingText, 'busy')
    try {
      let res
      try {
        res = await call('/decision', { items, note: noteText(), send: sendIt })
      } catch (refused) {
        if (!sendIt || refused.code !== 'AIAGENT_REVIEW_UNCONFIRMED') throw refused
        res = await sendAgain(refused.message)
        if (!res) { sendState.hidden = true; busy = false; paint(); return }
      }
      queue.textContent = res.reply
      ready = false
      confirmBtn.disabled = true
      let msg
      if (sendIt) msg = sentText(res)
      else { copyText(res.reply, { textContent: '' }); msg = tr('คัดลอกข้อความตอบกลับแล้ว — วางใน session ได้เลย') }
      showSend(msg, 'ok')
      toast(msg)
      refreshReviews()
      if (sendIt) sentToSession(r)
    } catch (e) {
      showSend(failText(e), 'bad')
      toast(failText(e), true)
      busy = false
      paint()
    }
  }
  note.addEventListener('input', () => { if (confirmed) later() })
  // a picture added or removed changes the message waiting to be sent
  function picsChanged () { paint(); if (confirmed && items && items.length) later() }

  if (st.reviewMsg) window.removeEventListener('message', st.reviewMsg)
  st.reviewMsg = e => {
    if (e.source !== $('reviewFrame').contentWindow || !e.data || e.data.aiagentReview !== true) return
    items = e.data.items || []
    paint()
    if (confirmed) later()
  }
  window.addEventListener('message', st.reviewMsg)

  // A room's page the admin has approved can be handed to the admin's own
  // session, which is the one allowed to push and open the merge request. It
  // is a second, separate act: approving answers the room and nothing more.
  const adminSess = isUser() ? [] : st.snap.sessions.filter(x => x.protected && x.live && x.messageVia)
  let handOver = null
  if (r.owner && r.status === 'approved' && adminSess.length) {
    const pick = h('select', { 'aria-label': tr('session ของ admin') }, ...adminSess.map(x => h('option', { value: x.sessionId }, x.displayName)))
    handOver = h('div', { class: 'handOver' },
      h('b', null, tr('ส่งต่อให้ session admin')),
      h('p', { class: 'note' }, tr('ให้ session ของ admin ตรวจ diff แล้ว push และเปิด merge request ตามกติกาของ repo การกดผ่านอย่างเดียวไม่ได้ push')),
      adminSess.length > 1 ? pick : null,
      h('button', { type: 'button', class: 'pri', onclick: () => onceFor('handover:' + r.id, async () => {
        const target = adminSess.find(x => x.sessionId === pick.value) || adminSess[0]
        const text = tr('คำขอจากห้อง ') + r.owner + tr(' ผ่านการตรวจบนเว็บแล้ว: ') + r.title + (r.summary ? '\n' + r.summary : '') +
          tr('\nงานอยู่ในห้องนั้น (commit ในเครื่อง) ให้ตรวจ diff ทุกไฟล์ secret และ version ที่ bump แล้ว push และเปิด merge request ตามกติกาของ repo นี้ ถ้าพบปัญหาให้หยุดและรายงาน')
        if (!await confirmBox(tr('ส่งต่อให้ ') + target.displayName, tr('session นี้จะได้รับคำสั่งให้ตรวจ แล้ว push และเปิด merge request'), text, tr('ส่งคำสั่ง'))) return
        if (await act('/sessions/' + encodeURIComponent(target.sessionId) + '/message', { message: text, sendId: newSendId() }, () => tr('ส่งคำสั่งให้ ') + target.displayName + tr(' แล้ว'))) $('reviewDlg').close()
      }) }, tr('ให้ admin push และเปิด MR')))
  }
  $('reviewSide').replaceChildren(...[
    h('dl', { class: 'kv' },
      h('dt', null, tr('จาก')), h('dd', null, (r.owner ? tr('จากห้อง ') + r.owner + ' · ' : '') + (sess ? sess.displayName : (r.runtime === 'codexCli' ? 'Codex' : tr('ไม่ระบุ session')))),
      h('dt', null, tr('ส่งเมื่อ')), h('dd', null, fmtTime(r.createdAt) + ' · ' + ago(r.createdAt)),
      h('dt', null, tr('สถานะ')), h('dd', null, r.status === 'open' ? tr('รอตรวจ') : r.status === 'approved' ? tr('ตกลงแล้ว') : tr('ขอแก้แล้ว')),
      r.cwd ? h('dt', null, tr('โฟลเดอร์')) : null, r.cwd ? h('dd', { class: 'mono' }, r.cwd) : null,
      r.sessionId ? h('dt', null, tr('session')) : null, r.sessionId ? h('dd', { class: 'mono' }, r.sessionId.slice(0, 8)) : null,
      r.sentAt ? h('dt', null, tr('ส่งให้ agent')) : null,
      r.sentAt ? h('dd', null, fmtTime(r.sentAt) + ' · ' + (r.sentVia === 'queued' ? tr('รอส่งเมื่อ agent ว่าง') : r.sentVia + (r.delivery === 'acked' ? tr(' · ได้รับแล้ว') : r.delivery === 'held' ? tr(' · อยู่ในคิวของ agent แล้ว') : r.delivery === 'unknown' ? tr(' · ยังไม่ยืนยันว่าถึง agent') : ''))) : null),
    r.summary ? h('p', null, r.summary) : null,
    status,
    note,
    picRow,
    itemsBox,
    legacyBox,
    sendState,
    handOver,
    canSend ? null : h('p', { class: 'note' }, cantSendWhy),
    unconfirmed ? h('div', { class: 'revBtns' }, h('button', { type: 'button', class: 'warn', onclick: e => { const btn = e.currentTarget; onceFor('review:' + r.id, async () => {
      btn.disabled = true
      try {
        const res = await sendAgain(tr('ผลตรวจนี้พิมพ์ให้ agent ไปแล้วแต่ยังไม่ยืนยันว่าถึง ถ้าเปิดดูแล้วไม่ถึงให้กด "ส่งซ้ำ"'))
        if (res) sentAgain(res)
      } catch (err) {
        showSend(failText(err), 'bad')
        toast(failText(err), true)
      } finally { btn.disabled = false }
    }) } }, tr('ส่งซ้ำ'))) : null,
    canSend && !(sess && sess.live) ? h('p', { class: 'note' }, tr('session นี้ไม่ได้รันอยู่ — กดส่งแล้ว aiagent จะเปิด session ต่อ (claude --resume) ในโฟลเดอร์ของมันแล้วพิมพ์ข้อความให้')) : null,
    out].filter(Boolean))
  paint()
  try {
    const t = await api('/reviews/' + encodeURIComponent(id) + '/view', { method: 'POST' })
    $('reviewFrame').src = t.url
  } catch (e) { toast(tr('เปิดหน้าตรวจไม่ได้: ') + e.message, true) }
  $('reviewDlg').showModal()
}

// The review dialog's two views: the page the agent wrote, and the change it
// describes (the commit `aiagent review --repo` recorded), read from the
// room's checkout. The preview button opens what the room is running now.
function reviewRoom (r) {
  if (!st.me || !st.me.preview) return ''
  return r.owner || (isUser() ? st.me.osUser : st.as) || ''
}
function reviewTabs (r) {
  const page = $('rvTabPage'); const code = $('rvTabCode'); const box = $('reviewCode')
  let loaded = false
  const show = which => {
    page.setAttribute('aria-selected', String(which === 'page'))
    code.setAttribute('aria-selected', String(which === 'code'))
    $('reviewFrame').hidden = which !== 'page'
    box.hidden = which !== 'code'
    if (which === 'code' && !loaded) { loaded = true; loadReviewCode(r, box) }
  }
  page.onclick = () => show('page')
  code.onclick = () => show('code')
  const room = reviewRoom(r)
  $('rvPreview').hidden = !room
  $('rvPreview').onclick = () => openPreview(room)
  box.replaceChildren()
  show('page')
}
async function loadReviewCode (r, box) {
  box.replaceChildren(h('p', { class: 'tmeta' }, tr('กำลังอ่านโค้ด…')))
  let d
  try { d = await api('/reviews/' + encodeURIComponent(r.id) + '/code') } catch (e) {
    box.replaceChildren(h('div', { class: 'twarn' }, tr('อ่านโค้ดไม่ได้: ') + e.message)); return
  }
  const c = d.code
  const head = c ? h('div', { class: 'rvCodeHead' },
    h('div', null, h('small', null, tr('repo')), ' ', h('code', null, c.repo)),
    c.branch ? h('div', null, h('small', null, tr('branch')), ' ', h('code', null, c.branch)) : null,
    h('div', null, h('small', null, tr('เทียบ')), ' ', h('code', null, (c.baseRef || '') + ' ' + c.base.slice(0, 8)), ' → ', h('code', null, c.commit.slice(0, 8))),
    d.branchHead ? h('div', { class: 'twarn' }, tr('branch นี้มี commit ใหม่กว่าหน้าที่ส่งตรวจ (') + d.branchHead.slice(0, 8) + tr(') — ที่เห็นคือโค้ด ณ ตอนส่งตรวจ')) : null) : null
  if (!c) {
    box.replaceChildren(h('div', { class: 'rvCard unknown wide' }, h('b', null, tr('ไม่มีโค้ดให้ดู')), h('div', { class: 'tmeta' }, tr('ให้ agent ส่งหน้าตรวจใหม่ด้วย aiagent review --repo <โฟลเดอร์ repo> แล้วแท็บนี้จะแสดง diff ของ commit นั้น'))))
    return
  }
  const v = { files: d.files, diff: d.diff, diffTruncated: d.diffTruncated, diffError: d.error || '' }
  box.replaceChildren(...[head, reviewFiles(v, box), reviewDiff(v)].filter(Boolean))
}

// ----- accounts: switch the Claude / Codex sign-in on this box -----
const accountName = { claude: 'Claude Code', codex: 'Codex', agy: 'Google Antigravity' }
$('accTop').addEventListener('click', () => {
  const dlg = $('accountsDlg')
  dlg.scrollTo({ top: 0, behavior: 'smooth' })
  for (const el of dlg.querySelectorAll('.spBody')) if (el.scrollTop) el.scrollTo({ top: 0, behavior: 'smooth' })
})
$('accClose').addEventListener('click', () => $('accountsDlg').close())
$('accLogout').addEventListener('click', async () => {
  if (!await confirmBox(tr('ออกจากระบบ'), tr('หน้านี้จะลืม token และกลับไปหน้าเข้าสู่ระบบ token ยังใช้ได้ตามเดิม'), '', tr('ออกจากระบบ'))) return
  signOut()
})
// Sign this tab out: forget the token here (both storages) and show the login.
// The token itself stays valid; revoking it is done on the tokens page.
function signOut () {
  st.token = ''
  st.me = null
  st.snap = null
  safeSet(tokenKey, '')
  safeSet(roleKey, '')
  showMenu('')
  for (const d of document.querySelectorAll('dialog[open]')) d.close()
  showLogin()
}
$('accJump').addEventListener('click', e => {
  const id = e.target && e.target.dataset && e.target.dataset.jump
  if (id && $(id)) $(id).scrollIntoView({ behavior: 'smooth', block: 'start' })
})
async function openAccounts () {
  $('accountsDlg').showModal()
  drawUpdate()
  await drawAccounts()
  await drawCLIs()
  await drawTokens()
  await drawVault()
  await drawEnvironments()
  await drawRooms()
}

// ----- vault: access tokens for GitLab, GitHub and others. A value is shown to
// the server once and never read back; the list shows names and purposes only.
// What was typed survives a refused save: only a saved form is cleared.
async function drawVault (editing) {
  if (!$('accountsDlg').open) return
  let list
  try { list = (await api('/vault')).entries } catch (e) { $('vaultBody').replaceChildren(h('p', { class: 'note' }, tr('อ่านรายการ token ไม่ได้: ') + e.message)); return }
  const name = h('input', { type: 'text', id: 'vaultName', maxlength: '60', placeholder: tr('ชื่อ เช่น gitlab-netra-ui'), autocomplete: 'off' })
  const prov = h('select', { id: 'vaultProv', 'aria-label': tr('ประเภท') }, h('option', { value: 'gitlab' }, 'GitLab'), h('option', { value: 'github' }, 'GitHub'), h('option', { value: 'other' }, tr('อื่นๆ')))
  const purpose = h('input', { type: 'text', id: 'vaultPurpose', maxlength: '300', placeholder: tr('ใช้กับอะไร เช่น push netra-ui และเปิด MR'), autocomplete: 'off' })
  const value = h('input', { type: 'password', id: 'vaultValue', maxlength: '400', placeholder: editing ? tr('ค่า token ใหม่ (เว้นว่าง = ใช้ค่าเดิม)') : tr('ค่า token (แสดงครั้งเดียว ไม่มีการอ่านกลับ)'), autocomplete: 'new-password', spellcheck: 'false' })
  const err = h('p', { class: 'vaultErr', role: 'alert' })
  if (editing) { name.value = editing.name; prov.value = editing.provider; purpose.value = editing.purpose }
  const save = async () => {
    err.textContent = ''
    try {
      await api(editing ? '/vault/' + editing.id + '/edit' : '/vault', { method: 'POST', body: { name: name.value.trim(), provider: prov.value, purpose: purpose.value.trim(), value: value.value.trim() } })
    } catch (e) { err.textContent = tr('ไม่สำเร็จ: ') + e.message; return }
    toast(tr('บันทึก token แล้ว'))
    drawVault()
  }
  const remove = async e => {
    if (!await confirmBox(tr('ลบ token'), tr('ระบบที่ใช้ token นี้จะใช้ไม่ได้จนกว่าจะใส่ใหม่'), e.name, tr('ลบ'))) return
    try { await api('/vault/' + e.id + '/remove', { method: 'POST', body: {} }) } catch (x) { toast(tr('ไม่สำเร็จ: ') + x.message, true) }
    drawVault()
  }
  $('vaultBody').replaceChildren(h('div', { class: 'accCard' },
    h('div', { class: 'accTop' }, h('b', null, tr('Token ของบริการภายนอก'))),
    list.length ? h('ul', null, ...list.map(e => h('li', null, h('b', null, e.name), ' · ' + e.provider + ' · ' + e.purpose,
      e.lastUsed ? h('span', { class: 'note' }, ' · ' + tr('ใช้ล่าสุด ') + ago(e.lastUsed)) : null, ' ',
      h('button', { type: 'button', class: 'ghost', onclick: () => drawVault(e) }, tr('แก้ไข')), ' ',
      h('button', { type: 'button', class: 'warn', onclick: () => remove(e) }, tr('ลบ'))))) : h('p', { class: 'note' }, tr('ยังไม่มี token')),
    h('b', null, editing ? tr('แก้ไข ') + editing.name : tr('เพิ่ม token')),
    h('div', { class: 'accRow' }, name, prov),
    h('div', { class: 'accRow' }, purpose),
    h('div', { class: 'accRow' }, value, h('button', { type: 'button', class: 'pri', onclick: save }, tr('บันทึก')), editing ? h('button', { type: 'button', class: 'ghost', onclick: () => drawVault() }, tr('ยกเลิก')) : null),
    err,
    h('p', { class: 'note' }, tr('ชื่อ: ตัวอักษร ตัวเลข เว้นวรรค . _ / - ไม่เกิน 60 ตัว · ใช้กับอะไร: 3-300 ตัว · ค่า token: 16-400 ตัว ไม่มีเว้นวรรค')),
    h('p', { class: 'note' }, tr('ห้องของ user ไม่เห็นค่า token นี้ ใช้ได้เฉพาะระบบของ admin เช่น การส่ง MR และ CI'))))
}

// ----- environments: which environment is dev, uat or prod, and the variables
// a program there needs. A value is sent once and never read back; the list
// says only whether it is set. dev and uat go to the builders' rooms as files,
// prod never leaves the admin (docs/ENVIRONMENTS.md).
// st.edit: the environment being edited; st.open: the one whose variables
// show; st.editVar: the variable being changed.
const envTierLabel = { dev: 'dev', uat: 'uat', prod: 'prod' }
async function drawEnvironments (st) {
  if (!$('accountsDlg').open) return
  st = st || {}
  const box = $('envBody')
  let list
  try { list = (await api('/environments')).environments } catch (e) { box.replaceChildren(h('p', { class: 'note' }, tr('อ่านรายการ environment ไม่ได้: ') + e.message)); return }
  const badge = t => h('span', { class: 'envTier tier-' + t }, envTierLabel[t] || t)
  const reach = t => t === 'prod' ? tr('ไม่ส่งให้ห้องใดเลย งาน prod ผ่าน admin') : tr('ส่งให้ห้อง frontend, backend, fullstack และ admin')
  const name = h('input', { type: 'text', id: 'envName', maxlength: '40', placeholder: tr('ชื่อ เช่น netra-dev'), autocomplete: 'off', spellcheck: 'false' })
  const tier = h('select', { id: 'envTier', 'aria-label': tr('tier') }, h('option', { value: 'dev' }, 'dev'), h('option', { value: 'uat' }, 'uat'), h('option', { value: 'prod' }, 'prod'))
  const note = h('input', { type: 'text', id: 'envNote', maxlength: '300', placeholder: tr('หมายเหตุ เช่น cluster ไหน ใครดูแล'), autocomplete: 'off' })
  const err = h('p', { class: 'vaultErr', role: 'alert' })
  if (st.edit) { name.value = st.edit.name; tier.value = st.edit.tier; note.value = st.edit.note || '' }
  const save = async () => {
    err.textContent = ''
    try {
      await api(st.edit ? '/environments/' + st.edit.id + '/edit' : '/environments', { method: 'POST', body: { name: name.value.trim(), tier: tier.value, note: note.value.trim() } })
    } catch (e) { err.textContent = tr('ไม่สำเร็จ: ') + e.message; return }
    toast(tr('บันทึก environment แล้ว'))
    drawEnvironments({ open: st.open })
  }
  const remove = async en => {
    if (!await confirmBox(tr('ลบ environment'), tr('ตัวแปรและค่าทั้งหมดของ environment นี้จะหายไป และไฟล์ในห้องจะถูกลบในรอบถัดไป'), en.name, tr('ลบ'))) return
    try { await api('/environments/' + en.id + '/remove', { method: 'POST', body: {} }) } catch (x) { toast(tr('ไม่สำเร็จ: ') + x.message, true) }
    drawEnvironments()
  }
  const varsOf = en => {
    const key = h('input', { type: 'text', id: 'envVarKey', maxlength: '64', placeholder: tr('ชื่อตัวแปร เช่น DATABASE_URL'), autocomplete: 'off', spellcheck: 'false' })
    const desc = h('input', { type: 'text', id: 'envVarDesc', maxlength: '200', placeholder: tr('ใช้ทำอะไร'), autocomplete: 'off' })
    const value = h('input', { type: 'password', id: 'envVarValue', maxlength: '4096', placeholder: st.editVar ? tr('ค่าใหม่ (เว้นว่าง = ใช้ค่าเดิม)') : tr('ค่า (ส่งครั้งเดียว ไม่มีการอ่านกลับ)'), autocomplete: 'new-password', spellcheck: 'false' })
    const verr = h('p', { class: 'vaultErr', role: 'alert' })
    if (st.editVar) { key.value = st.editVar.key; key.disabled = true; desc.value = st.editVar.description || '' }
    const saveVar = async () => {
      verr.textContent = ''
      try {
        await api('/environments/' + en.id + '/vars', { method: 'POST', body: { key: key.value.trim(), description: desc.value.trim(), value: value.value } })
      } catch (e) { verr.textContent = tr('ไม่สำเร็จ: ') + e.message; return }
      value.value = ''
      toast(tr('บันทึกตัวแปรแล้ว'))
      drawEnvironments({ open: en.id })
    }
    const removeVar = async v => {
      if (!await confirmBox(tr('ลบตัวแปร'), tr('ค่าของตัวแปรนี้จะหายไป และหายจากไฟล์ในห้องในรอบถัดไป'), v.key, tr('ลบ'))) return
      try { await api('/environments/' + en.id + '/vars/' + encodeURIComponent(v.key) + '/remove', { method: 'POST', body: {} }) } catch (x) { toast(tr('ไม่สำเร็จ: ') + x.message, true) }
      drawEnvironments({ open: en.id })
    }
    return h('div', { class: 'envVars' },
      en.vars.length ? h('ul', null, ...en.vars.map(v => h('li', null, h('code', null, v.key),
        v.description ? ' · ' + v.description : '', ' ',
        h('span', { class: 'envSet ' + (v.set ? 'on' : 'off') }, v.set ? tr('ตั้งค่าแล้ว') : tr('ยังไม่ตั้งค่า')), ' ',
        h('button', { type: 'button', class: 'ghost', onclick: () => drawEnvironments({ open: en.id, editVar: v }) }, tr('เปลี่ยนค่า')), ' ',
        h('button', { type: 'button', class: 'warn', onclick: () => removeVar(v) }, tr('ลบ'))))) : h('p', { class: 'note' }, tr('ยังไม่มีตัวแปร')),
      h('div', { class: 'accRow' }, key, desc),
      h('div', { class: 'accRow' }, value, h('button', { type: 'button', class: 'pri', onclick: saveVar }, st.editVar ? tr('บันทึก') : tr('เพิ่มตัวแปร')), st.editVar ? h('button', { type: 'button', class: 'ghost', onclick: () => drawEnvironments({ open: en.id }) }, tr('ยกเลิก')) : null),
      verr,
      h('p', { class: 'note' }, tr('ชื่อตัวแปร: A-Z 0-9 _ ขึ้นต้นด้วยตัวอักษร · ค่า: บรรทัดเดียว ไม่เกิน 4096 ไบต์ · หน้าเว็บไม่แสดงค่าที่บันทึกแล้ว')))
  }
  box.replaceChildren(h('div', { class: 'accCard' },
    h('div', { class: 'accTop' }, h('b', null, tr('Environment'))),
    list.length ? h('ul', { class: 'envList' }, ...list.map(en => h('li', null,
      h('b', null, en.name), ' ', badge(en.tier), en.note ? ' · ' + en.note : '',
      h('span', { class: 'note' }, ' · ' + en.vars.length + tr(' ตัวแปร') + ' · ' + reach(en.tier)), ' ',
      h('button', { type: 'button', class: 'ghost', onclick: () => drawEnvironments({ open: st.open === en.id ? '' : en.id }) }, st.open === en.id ? tr('ซ่อนตัวแปร') : tr('ตัวแปร')), ' ',
      h('button', { type: 'button', class: 'ghost', onclick: () => drawEnvironments({ edit: en, open: st.open }) }, tr('แก้ไข')), ' ',
      h('button', { type: 'button', class: 'warn', onclick: () => remove(en) }, tr('ลบ')),
      st.open === en.id ? varsOf(en) : null))) : h('p', { class: 'note' }, tr('ยังไม่มี environment')),
    h('b', null, st.edit ? tr('แก้ไข ') + st.edit.name : tr('เพิ่ม environment')),
    h('div', { class: 'accRow' }, name, tier),
    h('div', { class: 'accRow' }, note, h('button', { type: 'button', class: 'pri', onclick: save }, tr('บันทึก')), st.edit ? h('button', { type: 'button', class: 'ghost', onclick: () => drawEnvironments({ open: st.open }) }, tr('ยกเลิก')) : null),
    err,
    h('p', { class: 'note' }, tr('ชื่อ: a-z 0-9 และขีดกลาง ไม่เกิน 40 ตัว · dev และ uat: ห้อง frontend, backend, fullstack และ admin ได้รับเป็นไฟล์ ~/.config/aiagent/env/<ชื่อ>.env · ห้อง tester ไม่ได้รับ')),
    h('p', { class: 'note' }, tr('prod: บันทึกไว้ให้ admin รู้ว่าอันไหนคือ prod ค่าของ prod ไม่ถูกส่งให้ห้องใดเลย'))))
}

// ----- user tokens: the server refuses these calls without an admin token -----
async function drawRooms () {
  const box = $('roomBody')
  let r
  try { r = await api('/rooms') } catch (e) { box.replaceChildren(); return }
  const on = h('input', { type: 'checkbox', id: 'roomOn' })
  on.checked = r.policy.enabled
  const pct = h('input', { type: 'number', id: 'roomPct', min: 10, max: 99, step: 1, value: r.policy.weeklyPct })
  const save = async () => {
    try { await api('/rooms', { method: 'POST', body: { enabled: on.checked, weeklyPct: Number(pct.value) } }); toast(tr('บันทึกแล้ว มีผลภายใน 1 นาที')) } catch (e) { toast(tr('ไม่สำเร็จ: ') + e.message, true) }
    drawRooms()
  }
  box.replaceChildren(h('div', { class: 'accCard' },
    h('div', { class: 'accTop' }, h('b', null, tr('Model ของห้อง user'))),
    h('p', null, tr('ตอนนี้: ') + modelName(r.model || 'opus') + ' · ' + tr('ใช้รายสัปดาห์: ') + (r.weekly === undefined ? tr('ไม่ทราบ') : Math.round(r.weekly) + '%')),
    h('label', { class: 'polRow' }, on, h('span', null, tr('เปลี่ยนเป็น ') + modelName(r.lowModel) + tr(' เมื่อรายสัปดาห์เกิน')), pct, h('span', null, '%')),
    h('p', { class: 'note' }, tr('ปกติใช้ Opus รุ่นล่าสุด user เปลี่ยน model เองไม่ได้ ระบบสลับเมื่อห้องว่างและคุยต่อจากเดิม ถ้าไม่ทราบค่าจะใช้ Opus')),
    h('div', { class: 'accRow' }, h('button', { type: 'button', class: 'pri', onclick: save }, tr('บันทึก')))))
}
// ----- the phone app: a token just made is handed over as a pairing code -----
// Never the token itself in a QR: the code holds a one-time ticket (pair.go,
// POST /tokens/:id/pair), ten minutes, and the phone that scans it gets a
// device token of its own in that user's scope. The QR is drawn by this
// service, never by an outside one.
async function freshPairCard (fresh) {
  const p = await api('/tokens/' + encodeURIComponent(fresh.id) + '/pair', { method: 'POST', body: { token: fresh.token, origin: location.origin } })
  // never count past ten minutes, even if this computer's clock runs behind
  const ends = Math.min(Date.parse(p.expiresAt), Date.now() + 10 * 60 * 1000)
  const img = h('img', { src: p.qr, alt: tr('QR รหัสจับคู่แอป'), class: 'connectQR' })
  const left = h('p', { class: 'pairLeft' })
  const save = h('a', { class: 'pairSave', download: 'aiagent-pair.png', href: p.qr }, tr('บันทึกภาพ'))
  let timer = 0
  const tick = () => {
    const sec = Math.max(0, Math.round((ends - Date.now()) / 1000))
    if (!left.isConnected && timer) { clearInterval(timer); return }
    if (sec > 0) {
      left.textContent = tr('ใช้ได้อีก ') + Math.floor(sec / 60) + ':' + String(sec % 60).padStart(2, '0')
      return
    }
    clearInterval(timer)
    img.classList.add('expired')
    save.remove()
    left.textContent = tr('รหัสนี้หมดอายุแล้ว กดออก token ใหม่')
  }
  tick()
  timer = setInterval(tick, 1000)
  return h('div', { class: 'connectCard' }, img,
    h('div', null,
      h('p', null, tr('ให้ผู้ใช้สแกน QR ด้วยแอป AI Agent World หรือบันทึกภาพแล้วส่งให้ทางช่องทางส่วนตัว')),
      left,
      h('div', { class: 'accRow pairRow' }, save),
      h('p', { class: 'note' }, tr('รหัสนี้ใช้ได้ครั้งเดียว ภายใน 10 นาที แอปจะได้ token ของตัวเอง สิทธิ์เท่ากับผู้ใช้นี้'))))
}
// The header's phone dialog shows a one-time pairing code (pair.go), never the
// token: the QR holds a ticket the app trades once for the token, within ten
// minutes, so a photo of it opens nothing afterwards.
function openConnect () {
  // the reach card makes the dialog tall: side by side on a wide screen
  $('connectDlg').classList.toggle('wide', phoneCannotReach())
  $('connectDlg').showModal()
  $('connectReach').replaceChildren(...(phoneCannotReach() ? [reachCard()] : []))
  drawPair()
}
// The pairing QR carries the page's own origin. Opened as 127.0.0.1, localhost
// or plain http, that is an address the phone cannot open (the app takes
// https only), so the dialog first says how to get one. On an https address
// that is not loopback nothing changes.
function phoneCannotReach () {
  const host = location.hostname
  return location.protocol !== 'https:' || host === 'localhost' || host === '127.0.0.1' || host === '::1' || host === '[::1]'
}
// Instructions an agent can follow (docs/PUBLIC_URL.md). They name this
// page's address and nothing secret: never the token, never a credential.
// The link is the copy tagged with the running version, not main: the agent
// runs what it says, so a later change to the branch must not reach it.
function publicUrlDoc () {
  const v = st.snap && st.snap.version
  return v ? 'docs/PUBLIC_URL.md (https://github.com/hotkhwan/aiagent/blob/v' + v + '/docs/PUBLIC_URL.md)' : 'docs/PUBLIC_URL.md'
}
function reachWays () {
  const where = tr('\naiagent นี้เปิดอยู่ที่ ') + location.origin + '/aiagent/'
  return [
    { id: 'try', label: tr('ลองตอนนี้'), about: tr('Cloudflare Quick Tunnel ไม่ต้องมีบัญชี ได้ที่อยู่สุ่ม *.trycloudflare.com ที่เปลี่ยนทุกครั้งที่เริ่มใหม่'),
      text: tr('ทำตาม ') + publicUrlDoc() + tr(' หัวข้อ A เพื่อเปิด Cloudflare Quick Tunnel ชั่วคราวให้ aiagent นี้ ให้มือถือเปิดผ่าน https ได้ บอกฉันก่อนว่าจะเปิดอะไรออกอินเทอร์เน็ตและรอให้ฉันตอบตกลง เปิดเฉพาะ aiagent เท่านั้น ห้ามเปิด ssh หรือพอร์ตอื่น และห้ามแสดง token หรือ credential ใด ๆ') + where },
    { id: 'own', label: tr('ที่อยู่ของคุณเอง'), about: tr('Cloudflare Tunnel บนบัญชี Cloudflare ของคุณกับโดเมนของคุณ ที่อยู่ถาวร'),
      text: tr('ทำตาม ') + publicUrlDoc() + tr(' หัวข้อ B เพื่อให้ aiagent นี้มีที่อยู่ https สาธารณะบนบัญชี Cloudflare ของฉันเอง ถามฉันก่อนเปิดอะไรออกอินเทอร์เน็ต และหยุดรอให้ฉันล็อกอิน Cloudflare ในเบราว์เซอร์เอง ห้ามขอรหัสผ่านหรือ API token ห้ามเปิด ssh หรือพอร์ตอื่น และห้ามแสดง token หรือ credential ใด ๆ') + where },
    { id: 'cloud', label: tr('Cloud server'), about: tr('เครื่องที่มี IP สาธารณะ ใช้ reverse proxy ที่ทำ HTTPS ให้เอง เช่น Caddy'),
      text: tr('ทำตาม ') + publicUrlDoc() + tr(' หัวข้อ C เพื่อให้ aiagent บน server นี้เปิดผ่าน https ได้ ถามฉันก่อนเปิดพอร์ตหรือแก้ firewall เปิดเฉพาะพอร์ต 443 ห้ามแตะการตั้งค่า ssh ที่มีอยู่ ห้ามเปิดพอร์ต 8890 ออกนอกเครื่อง และห้ามแสดง token หรือ credential ใด ๆ') + where }
  ]
}
// Agents the person may message, by the same rule as the composer's picker
// (live, with a way to type to it). Only the admin is offered this: putting
// the machine on the internet is the admin's call, and a user's room may be
// someone else's sandbox. It sends through the ordinary message path, nothing
// the admin could not have typed into the chat.
function reachTargets () {
  if (isUser() || !st.snap) return []
  return (st.snap.sessions || []).filter(x => x.live && x.messageVia && !x.readOnly)
}
function reachCard () {
  const ways = reachWays()
  const targets = reachTargets()
  const btns = ways.map(w => h('button', { type: 'button', class: 'ghost', 'aria-pressed': 'false', onclick: () => pick(w) }, w.label))
  const about = h('p', { class: 'note' })
  const text = h('pre', { class: 'reachText' })
  const sel = h('select', { 'aria-label': tr('agent ที่จะส่งให้') }, ...targets.map(x => h('option', { value: x.sessionId }, x.displayName + ' · ' + (x.messageVia === 'relay' ? 'relay' : 'agcmd ' + x.dispatchVia))))
  let cur = ways[0]
  const copy = h('button', { type: 'button', class: 'ghost', onclick: e => copyText(cur.text, e.currentTarget) }, tr('คัดลอกคำสั่ง'))
  const send = targets.length ? h('button', { type: 'button', class: 'pri', onclick: () => sendReach(cur.text, targets, sel.value) }, tr('ส่งให้ agent')) : null
  function pick (w) {
    cur = w
    btns.forEach((b, i) => b.setAttribute('aria-pressed', ways[i] === w ? 'true' : 'false'))
    about.textContent = w.about
    text.textContent = w.text
  }
  pick(cur)
  return h('div', { class: 'reachCard' },
    h('b', null, tr('ทำให้มือถือเปิดถึงได้')),
    h('p', null, tr('หน้านี้เปิดจาก ') + location.origin + tr(' ซึ่งมือถือเปิดไม่ถึง แอปต้องใช้ที่อยู่ https ที่มือถือเข้าถึงได้ เลือกวิธี แล้วให้ agent ของคุณตั้งให้ หรือคัดลอกคำสั่งไปทำเอง')),
    h('div', { class: 'reachWays', role: 'group', 'aria-label': tr('วิธีเปิดให้มือถือเข้าถึง') }, ...btns),
    about, text,
    h('div', { class: 'accRow' }, targets.length > 1 ? sel : null, send, copy),
    targets.length ? null : h('p', { class: 'note' }, isUser() ? tr('ให้คนที่ดูแลเครื่องนี้ตั้งให้ หรือคัดลอกคำสั่งไปให้เขา') : tr('ไม่มี agent ที่ส่งข้อความจากเว็บได้ตอนนี้ คัดลอกคำสั่งไปวางใน agent เอง')),
    h('p', { class: 'note' }, tr('QR ด้านล่างสร้างจากที่อยู่นี้ จึงใช้บนมือถือไม่ได้ เปิดหน้านี้ผ่านที่อยู่ https ใหม่แล้วกด 📱 แอป อีกครั้ง')))
}
// A message the admin could have typed: confirmed first, then the same
// /sessions/<id>/message call the chat uses.
async function sendReach (text, targets, id) {
  const target = targets.find(x => x.sessionId === id) || targets[0]
  await onceFor('reach:' + target.sessionId, async () => {
    if (!await confirmBox(tr('ส่งให้ ') + target.displayName, tr('agent จะได้รับคำสั่งนี้ และต้องถามคุณก่อนเปิดอะไรออกอินเทอร์เน็ต'), text, tr('ส่งคำสั่ง'))) return
    await act('/sessions/' + encodeURIComponent(target.sessionId) + '/message', { message: text, sendId: newSendId() }, () => tr('ส่งคำสั่งให้ ') + target.displayName + tr(' แล้ว'))
  })
}
async function drawPair () {
  clearInterval(st.pairTimer)
  const seq = st.pairSeq = (st.pairSeq || 0) + 1
  const box = $('connectBody')
  const again = h('button', { type: 'button', class: 'ghost', onclick: () => drawPair() }, tr('รหัสใหม่'))
  box.replaceChildren(h('p', { class: 'note' }, tr('กำลังสร้างรหัส…')))
  let p
  try {
    p = await api('/connect/pair', { method: 'POST', body: { origin: location.origin } })
  } catch (e) {
    if (seq === st.pairSeq) box.replaceChildren(h('p', { class: 'note' }, tr('สร้างรหัสไม่ได้: ') + e.message), again)
    return
  }
  // a newer request, or a closed dialog, owns the box now
  if (seq !== st.pairSeq || !$('connectDlg').open) return
  // never count past ten minutes, even if this computer's clock runs behind
  const ends = Math.min(Date.parse(p.expiresAt), Date.now() + 10 * 60 * 1000)
  const img = h('img', { src: p.qr, alt: tr('QR รหัสจับคู่แอป'), class: 'connectQR' })
  const left = h('p', { class: 'pairLeft' })
  const save = h('a', { class: 'pairSave', download: 'aiagent-pair.png', href: p.qr }, tr('บันทึกภาพ'))
  const tick = () => {
    const sec = Math.max(0, Math.round((ends - Date.now()) / 1000))
    if (sec > 0) {
      left.textContent = tr('ใช้ได้อีก ') + Math.floor(sec / 60) + ':' + String(sec % 60).padStart(2, '0')
      return
    }
    clearInterval(st.pairTimer)
    img.classList.add('expired')
    save.remove()
    left.textContent = tr('รหัสนี้หมดอายุแล้ว กดรหัสใหม่')
  }
  box.replaceChildren(h('div', { class: 'connectCard' }, img,
    h('div', null,
      h('p', null, tr('สแกน QR ด้วยแอป AI Agent World หรือบันทึกภาพไว้ แล้วเปิดภาพจาก Photos หรือ Files ในแอป')),
      left,
      h('div', { class: 'accRow pairRow' }, save, again),
      h('p', { class: 'note' }, tr('รหัสนี้ใช้ได้ครั้งเดียว ภายใน 10 นาที แอปจะได้ token ของตัวเอง สิทธิ์เท่ากับหน้านี้ เพิกถอนได้จากปุ่มอุปกรณ์ที่จับคู่')))))
  tick()
  if (Date.now() < ends) st.pairTimer = setInterval(tick, 1000)
  drawDevices(box)
}
// The paired phones in this page's scope, each with its own token (the list
// carries none), and a way to end one: a lost phone costs one revoke. The
// dialog shows only a button with the count; the rows open in their own
// popup, so the pairing dialog keeps to one screen.
async function drawDevices (box) {
  const btn = h('button', { type: 'button', class: 'ghost devicesBtn' }, tr('📱 อุปกรณ์ที่จับคู่ ') + '…')
  box.append(btn)
  const body = $('devicesBody')
  let list = []
  const fill = () => {
    btn.textContent = tr('📱 อุปกรณ์ที่จับคู่ ') + '(' + list.length + ')'
    if (!list.length) {
      body.replaceChildren(h('p', { class: 'note' }, tr('ยังไม่มีอุปกรณ์ที่จับคู่')))
      return
    }
    body.replaceChildren(...list.map(d => {
      const revoke = h('button', { type: 'button', class: 'ghost' }, tr('เพิกถอน'))
      revoke.addEventListener('click', async () => {
        if (!await confirmBox(tr('เพิกถอนอุปกรณ์นี้?'), d.name + ' · ' + d.owner, '', tr('เพิกถอน'))) return
        try {
          await api('/connect/devices/' + encodeURIComponent(d.id) + '/revoke', { method: 'POST', body: {} })
        } catch (e) {
          body.append(h('p', { class: 'note' }, tr('เพิกถอนไม่ได้: ') + e.message))
          return
        }
        list = list.filter(x => x.id !== d.id)
        fill()
      })
      return h('div', { class: 'accRow deviceRow' },
        h('span', null, d.name + ' · ' + d.owner + (d.current ? ' (' + tr('เครื่องนี้') + ')' : ''),
          h('br'), h('small', null, tr('จับคู่เมื่อ ') + new Date(d.createdAt).toLocaleString())),
        revoke)
    }))
  }
  btn.addEventListener('click', () => { fill(); $('devicesDlg').showModal() })
  try {
    list = (await api('/connect/devices')).devices || []
  } catch (e) {
    btn.textContent = tr('อ่านรายการอุปกรณ์ไม่ได้: ') + e.message
    btn.disabled = true
    return
  }
  fill()
}
$('connectDlg').addEventListener('close', () => {
  clearInterval(st.pairTimer)
  // the code on screen is a key to this page's token: end it with the dialog
  api('/connect/pair/cancel', { method: 'POST' }).catch(() => {})
  st.pairSeq = (st.pairSeq || 0) + 1
  $('connectBody').replaceChildren()
  $('connectReach').replaceChildren()
})

async function drawTokens (fresh) {
  if (!$('accountsDlg').open) return
  let list, osUsers, duties, self
  try { const r = await api('/tokens'); list = r.tokens; osUsers = r.osUsers; duties = r.duties; self = r.self } catch (e) { $('tokenBody').replaceChildren(h('p', { class: 'note' }, tr('อ่านรายการ token ไม่ได้: ') + e.message)); return }
  const name = h('input', { type: 'text', id: 'tokenName', maxlength: '40', placeholder: osUsers ? tr('ชื่อผู้ใช้ (a-z, 0-9, -)') : tr('ชื่อผู้ใช้'), autocomplete: 'off' })
  const duty = dutySelect('', tr('บทบาทของห้องใหม่'))
  const create = async () => {
    try { drawTokens(await api('/tokens', { method: 'POST', body: { name: name.value.trim(), duty: duties ? duty.value : '' } })) } catch (e) { toast(tr('ไม่สำเร็จ: ') + e.message, true) }
  }
  const setDuty = async (t, value) => {
    if (value === 'admin' && !await confirmBox(tr('ตั้ง ') + t.name + tr(' เป็น admin'), tr('session ของห้องนี้จะอยู่ในรายชื่อ admin (protected.json) ที่ guard ของเครื่องอ่าน และห้องอื่นจะอ่านบทสนทนาของห้องนี้ไม่ได้'), t.name, tr('ตั้งเป็น admin'))) { drawTokens(); return }
    try { await api('/tokens/' + t.id + '/duty', { method: 'POST', body: { duty: value } }); toast(t.name + tr(' เป็น ') + dutyLabel(value)) } catch (e) { toast(tr('ไม่สำเร็จ: ') + e.message, true) }
    drawTokens()
  }
  const revoke = async t => {
    if (!await confirmBox(tr('เพิกถอน token'), t.osUser ? tr('ลบ OS user และหยุด agent ทุกตัวของผู้ใช้นี้ ไฟล์จะถูกเก็บสำรองไว้บนเครื่องก่อนลบ') : tr('ผู้ใช้นี้จะเข้าใช้งานไม่ได้ทันที'), t.name, tr('เพิกถอน'))) return
    try { await api('/tokens/' + t.id + '/revoke', { method: 'POST', body: {} }) } catch (e) { toast(tr('ไม่สำเร็จ: ') + e.message, true) }
    drawTokens()
  }
  const reissue = async t => {
    if (!await confirmBox(tr('ออก token ใหม่'), tr('token เดิมของผู้ใช้นี้จะใช้ไม่ได้ทันที ห้องและงานในห้องอยู่เหมือนเดิม token ใหม่แสดงครั้งเดียว'), t.name, tr('ออก token ใหม่'))) return
    try { drawTokens(await api('/tokens/' + t.id + '/reissue', { method: 'POST', body: {} })) } catch (e) { toast(tr('ไม่สำเร็จ: ') + e.message, true) }
  }
  // a fresh token also shows a one-time pairing code, for the user's phone
  const freshQR = fresh ? h('div', { class: 'connectSlot' }) : null
  if (fresh) freshPairCard(fresh).then(card => freshQR.replaceChildren(card), e => freshQR.replaceChildren(h('p', { class: 'note' }, tr('สร้าง QR ไม่ได้: ') + e.message)))
  $('tokenBody').replaceChildren(h('div', { class: 'accCard' },
    h('div', { class: 'accTop' }, h('b', null, tr('Token ของผู้ใช้'))),
    fresh ? h('p', null, fresh.name + ': ', h('code', { class: 'devCode' }, fresh.token), ' ', h('button', { type: 'button', class: 'ghost', onclick: e => copyText(fresh.token, e.currentTarget, true) }, tr('คัดลอก'))) : null,
    fresh ? h('p', { class: 'note' }, tr('token นี้แสดงครั้งเดียว ส่งให้ผู้ใช้ทางช่องทางส่วนตัว')) : null,
    freshQR,
    self ? h('ul', { class: 'selfToken' }, h('li', null, h('b', null, tr('token ของคุณ (admin)')), ' ', h('span', { class: 'note' }, self.room), ' ', roleBadge(self.duty || 'admin'))) : null,
    list.length ? h('ul', null, ...list.map(t => h('li', null, h('b', null, t.name), duties && t.osUser && !t.removing ? [' ', dutySelect(t.duty || '', tr('บทบาทของ ') + t.name, v => setDuty(t, v))] : null, t.removing ? h('span', { class: 'note' }, ' · ' + tr('ลบไม่สำเร็จ กดเพิกถอนอีกครั้ง')) : null, ' ', t.osUser && !t.removing ? h('button', { type: 'button', class: 'ghost', onclick: () => viewAs(t.osUser) }, tr('เข้าห้อง / login')) : null, ' ', t.osUser && !t.removing && st.me.preview ? h('button', { type: 'button', class: 'ghost', onclick: () => openPreview(t.osUser) }, tr('พรีวิว')) : null, ' ', t.removing ? null : h('button', { type: 'button', class: 'ghost', title: tr('ใช้เมื่อลืม token: ดู token เดิมไม่ได้ เพราะระบบเก็บแค่ hash'), onclick: () => reissue(t) }, tr('ออก token ใหม่')), ' ', h('button', { type: 'button', class: 'warn', onclick: () => revoke(t) }, tr('เพิกถอน'))))) : h('p', { class: 'note' }, tr('ยังไม่มี token ของผู้ใช้')),
    osUsers ? h('p', { class: 'note' }, tr('สร้าง token = สร้าง OS user และห้องทำงานแยกบนเครื่องนี้ ใช้เวลาราว 10 วินาที จากนั้นเข้าห้องเพื่อ login Claude ให้ผู้ใช้')) : null,
    duties ? h('p', { class: 'note' }, tr('บทบาท: dev หน้าเว็บ = รัน dev server ให้ดูที่ dev/<ห้อง> · backend = มีพรีวิวด้วย: dev server ของตัวเองที่ dev/<ห้อง> แสดง Swagger/OpenAPI หรือหน้าเล็ก ๆ ของตัวเอง พร้อมเทสต์ · fullstack = frontend + backend + service ที่ใช้ บน dev/UAT · tester = ไม่รันแอป ทดสอบบน UAT · admin = อยู่ในรายชื่อ admin ที่ guard อ่าน · ทุกบทบาทส่ง merge request ผ่าน admin · agent ของห้องอ่านบทบาทนี้จากคู่มือห้อง')) : null,
    h('div', { class: 'accRow' }, name, duties ? duty : null, h('button', { type: 'button', class: 'pri', onclick: create }, tr('สร้าง token')))))
}

// A room's duty: what the person behind the token does (roomduties.go).
function dutyLabel (v) {
  return { frontend: tr('dev หน้าเว็บ'), backend: tr('backend'), fullstack: tr('fullstack'), tester: tr('tester'), admin: tr('admin') }[v] || tr('ยังไม่กำหนดบทบาท')
}
// required: the admin's own token always has a duty (unset reads as admin)
function dutySelect (value, label, onchange, required) {
  return h('select', { 'aria-label': label, onchange: onchange ? e => onchange(e.target.value) : null },
    ...(required ? [] : ['']).concat(['frontend', 'backend', 'fullstack', 'tester', 'admin']).map(v => h('option', { value: v, selected: v === value }, dutyLabel(v))))
}

// ----- aiagent itself: see whether a newer release is out and install it -----
// The service fetches its own checkout's upstream; an update is offered only as
// a clean fast-forward. It restarts itself, so the page waits for the new
// version to answer and then reloads.
async function drawUpdate (fresh) {
  const box = $('updateBody')
  if (!box || !$('accountsDlg').open) return
  let u
  try { u = await api('/update' + (fresh ? '?fresh=1' : '')) } catch (e) { box.replaceChildren(h('div', { class: 'accCard' }, h('b', null, tr('อัปเดต aiagent')), h('p', { class: 'note' }, tr('ตรวจเวอร์ชันไม่ได้: ') + e.message))); return }
  const status = u.status || {}
  const run = async () => {
    const list = (u.commits || []).map(c => c.sha + '  ' + c.subject).join('\n')
    if (!await confirmBox(tr('อัปเดต aiagent เป็น v') + (u.latest || '?'), tr('ดึงโค้ดจาก origin ติดตั้ง แล้วเริ่ม service ใหม่ หน้านี้จะโหลดใหม่เองเมื่อเสร็จ (ราว 1-2 นาที)'), list, tr('อัปเดตเลย'))) return
    try { await api('/update', { method: 'POST', body: { sha: u.upstreamSha } }) } catch (e) { toast(tr('อัปเดตไม่ได้: ') + e.message, true); drawUpdate(true); return }
    waitForNewVersion(u.current)
    drawUpdate()
  }
  const head = h('div', { class: 'accTop' }, h('b', null, tr('อัปเดต aiagent')),
    h('button', { type: 'button', class: 'ghost', disabled: status.running, onclick: () => drawUpdate(true) }, tr('ตรวจอีกครั้ง')))
  const lines = [h('p', null, tr('เวอร์ชันนี้ v') + u.current + (u.latest && u.latest !== u.current ? tr(' · ล่าสุด v') + u.latest : ''))]
  if (status.running) lines.push(h('p', { class: 'note' }, tr('กำลังอัปเดต… หน้านี้จะโหลดใหม่เองเมื่อเสร็จ')))
  else if (u.error) lines.push(h('p', { class: 'note' }, u.error))
  else if (u.canUpdate) {
    lines.push(h('p', { class: 'note' }, tr('มีของใหม่ ') + u.behind + tr(' commit')))
    if ((u.commits || []).length) lines.push(h('ul', null, ...u.commits.map(c => h('li', null, h('code', null, c.sha), ' ' + c.subject))))
    lines.push(h('button', { type: 'button', class: 'pri', onclick: run }, tr('อัปเดตเป็น v') + (u.latest || '?')))
  } else if (u.reason) lines.push(h('p', { class: 'note' }, u.reason))
  const rel = u.release || {}
  if (rel.newer && !status.running) lines.push(h('p', null, h('button', { type: 'button', class: 'ghost', onclick: () => { $('accountsDlg').close(); openRelease() } }, tr('🔔 มี release ใหม่ ') + rel.latest)))
  if (!status.running && status.at && !status.ok && status.log) lines.push(h('details', null, h('summary', null, tr('การอัปเดตครั้งล่าสุดไม่สำเร็จ')), h('pre', { class: 'mono' }, status.log)))
  box.replaceChildren(h('div', { class: 'accCard' }, head, ...lines))
  if (status.running) { clearTimeout(st.updTimer); st.updTimer = setTimeout(() => drawUpdate(), 4000) }
}
// The service goes away while it restarts: keep asking until it answers with
// another version, then load the new page.
function waitForNewVersion (from) {
  const started = Date.now()
  const tick = async () => {
    if (Date.now() - started > 10 * 60 * 1000) return
    try {
      const snap = await api('/snapshot')
      if (snap.version && snap.version !== from) { location.reload(); return }
    } catch (e) { /* restarting */ }
    setTimeout(tick, 4000)
  }
  setTimeout(tick, 6000)
}

// ----- the 🔔: a newer published release, installed from its signed files -----
// The server asks GitHub at most every few hours; the dot shows when the latest
// release is newer than this one. Installing needs no Go and no checkout: the
// service downloads the release, checks its signature and checksums, swaps the
// binaries and restarts. The notes are the release's own text, shown as text.
async function checkRelease () {
  clearTimeout(st.relTimer)
  st.relTimer = setTimeout(checkRelease, 60 * 60 * 1000)
  let u
  try { u = await api('/update/release') } catch (e) { return }
  const r = u.release || {}
  $('releaseDot').hidden = !r.newer
  $('releaseBtn').title = r.newer ? tr('มีเวอร์ชันใหม่: ') + r.latest : tr('เวอร์ชันใหม่ของ aiagent')
}
function openRelease () {
  $('releaseDlg').showModal()
  drawRelease()
}
async function drawRelease (fresh) {
  const box = $('releaseBody')
  clearTimeout(st.relDrawTimer)
  if (!box || !$('releaseDlg').open) return
  let u
  try { u = await api('/update/release' + (fresh ? '?fresh=1' : '')) } catch (e) { box.replaceChildren(h('p', { class: 'note' }, tr('ตรวจเวอร์ชันไม่ได้: ') + e.message)); return }
  const r = u.release || {}
  const status = u.status || {}
  $('releaseDot').hidden = !r.newer
  const run = async () => {
    if (!await confirmBox(tr('อัปเดต aiagent เป็น ') + r.latest, tr('ดาวน์โหลด release ตรวจลายเซ็นและ checksum ติดตั้ง แล้วเริ่ม service ใหม่ หน้านี้จะโหลดใหม่เองเมื่อเสร็จ'), r.notes || '', tr('อัปเดตเลย'))) return
    try { await api('/update', { method: 'POST', body: { release: r.latest } }) } catch (e) { toast(tr('อัปเดตไม่ได้: ') + e.message, true); drawRelease(true); return }
    waitForNewVersion(u.current)
    drawRelease()
  }
  const lines = [h('p', null, tr('เวอร์ชันนี้ v') + u.current + (r.latest ? tr(' · ล่าสุด ') + r.latest : ''))]
  if (status.running) {
    lines.push(h('p', { class: 'note' }, tr('กำลังอัปเดต… หน้านี้จะโหลดใหม่เองเมื่อเสร็จ')))
    if (status.log) lines.push(h('pre', { class: 'mono' }, status.log))
  } else if (r.error) lines.push(h('p', { class: 'note' }, r.error))
  else if (r.newer) {
    if (r.notes) lines.push(h('pre', { class: 'relNotes' }, r.notes))
    lines.push(h('button', { type: 'button', class: 'pri', onclick: run }, tr('อัปเดตเป็น ') + r.latest))
  } else lines.push(h('p', { class: 'note' }, tr('เป็นเวอร์ชันล่าสุดแล้ว')))
  if (r.url) lines.push(h('p', { class: 'note' }, h('a', { href: r.url, target: '_blank', rel: 'noopener noreferrer' }, tr('ดู release บน GitHub'))))
  if (!status.running && status.at && !status.ok && status.log) lines.push(h('details', null, h('summary', null, tr('การอัปเดตครั้งล่าสุดไม่สำเร็จ')), h('pre', { class: 'mono' }, status.log)))
  lines.push(h('button', { type: 'button', class: 'ghost', disabled: status.running, onclick: () => drawRelease(true) }, tr('ตรวจอีกครั้ง')))
  box.replaceChildren(...lines)
  if (status.running) st.relDrawTimer = setTimeout(() => drawRelease(), 4000)
}

// ----- agent CLIs: one button updates every installed CLI, the model menu follows -----
async function drawCLIs () {
  clearTimeout(st.cliTimer)
  if (!$('accountsDlg').open) return
  let res
  try { res = await api('/clis') } catch (e) { $('cliBody').replaceChildren(h('p', { class: 'note' }, tr('อ่านรายการ CLI ไม่ได้: ') + e.message)); return }
  const { clis, update } = res
  const busy = update.running
  const run = async () => {
    if (!await confirmBox(tr('อัปเดต CLI ทั้งหมด'), tr('รันตัวอัปเดตของแต่ละ CLI ที่ติดตั้งบนเครื่องนี้ ใช้เวลาหลายนาที'), '', tr('เริ่มอัปเดต'))) return
    try { await api('/clis/update', { method: 'POST', body: {} }) } catch (e) { toast(tr('ไม่สำเร็จ: ') + e.message, true) }
    drawCLIs()
  }
  const line = (name, text, note) => h('li', null, h('b', null, name), ' ' + text, note ? h('span', { class: 'note' }, ' · ' + note) : null)
  $('cliBody').replaceChildren(h('div', { class: 'accCard' },
    h('div', { class: 'accTop' }, h('b', null, tr('CLI บนเครื่องนี้')), h('button', { type: 'button', class: 'pri', disabled: busy, onclick: run }, busy ? tr('กำลังอัปเดต…') : tr('อัปเดตทุก CLI'))),
    clis.length ? h('ul', null, ...clis.map(c => line(c.name, c.version || '?', c.method))) : h('p', { class: 'note' }, tr('ไม่พบ CLI ที่รู้จักบนเครื่องนี้')),
    update.results.length ? h('ul', null, ...update.results.map(r => line(r.name, (r.before || '?') + ' → ' + (r.after || '?'), r.error || tr('เสร็จ')))) : null,
    h('p', { class: 'note' }, tr('เมื่ออัปเดตเสร็จ รายการ model จะตามเวอร์ชันใหม่ของ CLI โดยไม่ต้องแก้โค้ด'))))
  if (st.cliBusy && !busy) st.models = null
  st.cliBusy = busy
  if (busy) st.cliTimer = setTimeout(drawCLIs, 2500)
}
async function drawAccounts () {
  clearTimeout(st.accTimer)
  if (!$('accountsDlg').open) return
  let accs
  try { accs = (await api('/accounts')).accounts } catch (e) { st.accSig = ''; $('accountsBody').replaceChildren(h('p', { class: 'note' }, tr('อ่านสถานะบัญชีไม่ได้: ') + e.message)); return }
  // The sign-in screen is polled while it runs, but rebuilding the card mid-flow
  // wiped the pasted code and swallowed the click on "confirm", so only a change
  // of shape redraws; the CLI's output is updated in place.
  const sig = accs.map(accSig).join('¦')
  if (sig !== st.accSig) {
    st.accSig = sig
    $('accountsBody').replaceChildren(...accs.map(accountCard).filter(Boolean), h('p', { class: 'note' }, tr('สลับบัญชี = เปิดหน้า login ของผู้ให้บริการ (ใช้ขั้นตอนของ CLI เอง ไม่ผ่าน shell) · ทุกขั้นบันทึก audit')))
  }
  for (const a of accs) setAccOut(a)
  const running = accs.some(a => a.login && a.login.running)
  // one more read after the flow ends: the CLI writes the new account as it exits
  if (running || st.accRunning) st.accTimer = setTimeout(drawAccounts, running ? 2500 : 1500)
  st.accRunning = running
}
function accSig (a) {
  const f = a.login || {}
  return [a.provider, a.loggedIn, a.email, a.method, a.org, a.expiresAt, a.detail, !!a.login, f.running, f.needsCode, f.url, f.code].join('·')
}
const accEls = {}
function setAccOut (a) {
  const el = accEls[a.provider]
  if (!el) return
  const text = (a.login && a.login.output) || ''
  el.out.textContent = text
  el.box.hidden = !text
  // while it runs the link and the code are what matter; once it has failed,
  // what the CLI said is the reason
  const failed = !!(a.login && !a.login.running)
  el.sum.textContent = failed ? tr('login ไม่สำเร็จ — ข้อความจาก CLI') : tr('ข้อความจาก CLI')
  if (failed) el.box.open = true
}
function accountCard (a) {
  const p = a.provider
  const f = a.login
  if (!accEls[p]) {
    accEls[p] = {
      email: h('input', { type: 'email', placeholder: tr('อีเมลบัญชีที่จะใช้ (ไม่บังคับ)'), autocomplete: 'off' }),
      code: h('input', { type: 'text', placeholder: tr('วางโค้ดที่ได้หลัง login'), autocomplete: 'off', spellcheck: 'false' }),
      out: h('pre', { class: 'accOut' }),
      sum: h('summary', null, '')
    }
    accEls[p].box = h('details', { class: 'accCli' }, accEls[p].sum, accEls[p].out)
  }
  const { email, code, box: out } = accEls[p]
  const call = async (path, body, msg) => {
    try { await api('/accounts/' + p + '/' + path, { method: 'POST', body: body || {} }); if (msg) toast(msg) } catch (e) { toast(tr('ไม่สำเร็จ: ') + e.message, true) }
    drawAccounts()
  }
  const start = async () => {
    const warn = p === 'codex'
      ? tr('Codex จะ logout ทันทีที่เริ่ม (เป็นพฤติกรรมของ codex เอง) — agent Codex ทุกตัวบนเครื่องจะใช้ไม่ได้จนกว่า login ใหม่เสร็จ')
      : p === 'agy' ? tr('login ด้วยบัญชี Google ของคุณเอง ข้อกำหนดของ Antigravity ห้ามใช้เครื่องมือภายนอกเข้าถึงบริการ ตรวจก่อนให้ agent อื่นสั่งงาน')
      : tr('บัญชีเดิมยังใช้ได้จนกว่าจะวางโค้ดสำเร็จ — หลังสลับ session ที่เปิดอยู่จะใช้บัญชีใหม่เมื่อรีเฟรช token')
    if (!await confirmBox(tr('สลับบัญชี ') + accountName[p], warn, a.email ? tr('ปัจจุบัน: ') + a.email : serverText(a.detail || ''), tr('เริ่ม login'))) return
    call('login', { email: email.value.trim() })
  }
  const logout = async () => {
    if (!await confirmBox('Logout ' + accountName[p], 'agent ' + accountName[p] + tr(' ทุกตัวบนเครื่องนี้จะหยุดใช้งานได้จนกว่าจะ login ใหม่'), a.email || a.method || '', 'Logout')) return
    call('logout', null, 'logout ' + accountName[p] + tr(' แล้ว'))
  }
  return h('div', { class: 'accCard' },
    h('div', { class: 'accTop' }, h('b', null, accountName[p]), h('span', { class: 'pill ' + (a.loggedIn ? 'normal' : 'reserve') }, h('i'), a.loggedIn ? tr('login อยู่') : tr('ยังไม่ได้ login'))),
    a.loggedIn ? h('p', null, [a.email, a.method, a.org].filter(Boolean).join(' · ') + (a.expiresAt ? tr(' · หมดอายุ ') + new Date(a.expiresAt).toLocaleString('th-TH', { dateStyle: 'short', timeStyle: 'short' }) : '')) : h('p', { class: 'note' }, a.detail ? serverText(a.detail) : tr('ยังไม่มีบัญชีบนเครื่องนี้ กด Login เพื่อเริ่ม')),
    f && f.running
      ? h('div', { class: 'accFlow' },
        h('p', null, tr('1) เปิดลิงก์นี้แล้ว sign in ด้วยบัญชีที่ต้องการ')),
        f.url ? h('a', { href: f.url, target: '_blank', rel: 'noopener noreferrer', class: 'pri accLink' }, tr('เปิดหน้า login ↗')) : h('p', { class: 'note' }, tr('กำลังรอลิงก์…')),
        f.code ? h('p', null, tr('2) กรอกรหัสนี้ในหน้านั้น: '), h('code', { class: 'devCode' }, f.code), ' ', h('button', { type: 'button', class: 'ghost', onclick: e => copyText(f.code, e.currentTarget) }, tr('คัดลอก'))) : null,
        p !== 'codex' ? h('div', { class: 'accRow' }, h('span', null, tr('2) วางโค้ดที่หน้าเว็บแสดง:')), code, h('button', { type: 'button', class: 'pri', onclick: () => call('code', { code: code.value }, tr('ส่งโค้ดแล้ว กำลังตรวจ…')) }, tr('ยืนยันโค้ด'))) : h('p', { class: 'note' }, tr('Codex จะเสร็จเองเมื่อกรอกรหัสบนเว็บแล้ว')),
        out,
        h('button', { type: 'button', class: 'ghost', onclick: () => call('cancel', null, tr('ยกเลิกแล้ว')) }, tr('ยกเลิก login')))
      : h('div', { class: 'accRow' },
        p === 'claude' ? email : null,
        p === 'agy' && a.loggedIn ? null : h('button', { type: 'button', class: 'pri', onclick: start }, a.loggedIn ? tr('สลับบัญชี') : 'Login'),
        a.loggedIn ? h('button', { type: 'button', class: 'warn', onclick: logout }, 'Logout') : null),
    f && !f.running ? out : null)
}
// ----- manuals and slides -----
async function downloadGuide (name) {
  try {
    const res = await fetch('/aiagent/api/guides/' + name, { headers: authHeaders(), cache: 'no-store' })
    if (!res.ok) throw new Error(String(res.status))
    const url = URL.createObjectURL(await res.blob())
    const a = h('a', { href: url, download: 'aiagent-' + name + '.pdf' })
    document.body.append(a); a.click(); a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 10000)
  } catch (e) { toast(tr('ดาวน์โหลดไม่สำเร็จ: ') + e.message, true) }
}
function helpMenu () {
  const old = document.querySelector('.helpPop')
  if (old) { old.remove(); return }
  const row = (name, label) => h('button', { type: 'button', class: 'ghost', onclick: () => { downloadGuide(name); pop.remove() } }, '⬇ ' + label)
  const pop = h('div', { class: 'qPop helpPop' },
    h('b', null, tr('สำหรับผู้ใช้')), row('user-manual', tr('คู่มือ (PDF)')), row('user-slides', tr('สไลด์ (PDF)')),
    ...(isUser() ? [] : [h('b', null, tr('สำหรับผู้ดูแล')), row('admin-manual', tr('คู่มือ (PDF)')), row('admin-slides', tr('สไลด์ (PDF)'))]))
  const at = $('helpBtn').getBoundingClientRect()
  pop.style.top = Math.round(at.bottom + 8) + 'px'
  pop.style.right = Math.max(8, Math.round(window.innerWidth - at.right)) + 'px'
  document.body.append(pop)
}
$('helpBtn').addEventListener('click', e => { e.stopPropagation(); helpMenu() })
document.addEventListener('click', e => { const p = document.querySelector('.helpPop'); if (p && !p.contains(e.target)) p.remove() })
$('accountsBtn').addEventListener('click', openAccounts)
$('releaseBtn').addEventListener('click', openRelease)
$('connectBtn').addEventListener('click', openConnect)
$('previewBtn').addEventListener('click', () => openPreview(previewRoom()))
$('asBack').addEventListener('click', () => viewAs(''))

// The agent's own face, cropped out of the walking sprite it already uses in
// the village, so the panel and the room show the same character (owner,
// 2026-11-13: "เอาหน้าตัวละครไปใส่ avatar ของ agent ทางขวา"). The rest sheet is
// four 128px frames in a row; this is the first frame, zoomed to the head.
const AVA_PX = 48      // .ava is 48px square
const AVA_ZOOM = 2.3   // how much of the 128px frame the head should fill
function avatarFace (sess) {
  const el = h('i', { class: 'avaFace' })
  const c = charFor(sess.sessionId)
  if (c.startsWith('cx-')) {
    el.style.backgroundImage = 'url(' + A(c) + ')'
    el.style.backgroundSize = (AVA_PX * 1.5) + 'px auto'
    el.style.backgroundPosition = 'center 25%'
    return el
  }
  // One frame, scaled. Percentages cannot express this: a negative background
  // position in % is measured against (box - image), which walks the sheet the
  // wrong way and lands on an empty frame.
  const f = AVA_PX * AVA_ZOOM
  el.style.backgroundImage = 'url(' + A('ch-' + c + '-rest') + ')'
  el.style.backgroundSize = (f * 4) + 'px ' + f + 'px'
  el.style.backgroundPosition = (AVA_PX / 2 - f / 2) + 'px ' + (AVA_PX / 2 - f * 0.28) + 'px'
  return el
}

// ----- pick the character that stands for one agent -----
let pickingFor = ''
function charPreview (c) {
  const el = h('i', { class: 'charImg' })
  if (c.startsWith('cx-')) {
    el.style.backgroundImage = 'url(' + A(c) + ')'
    el.style.backgroundSize = 'contain'
    el.style.backgroundPosition = 'center'
  } else {
    el.style.backgroundImage = 'url(' + A('ch-' + c + '-rest') + ')'
    el.style.backgroundSize = '400% 100%'
    el.style.backgroundPosition = '0 0'
  }
  return el
}

function openCharPicker (sess) {
  pickingFor = sess.sessionId
  const cur = charFor(sess.sessionId)
  $('charGrid').replaceChildren(...charPool.map(c => h('button', {
    type: 'button',
    class: 'charOpt' + (c === cur ? ' on' : ''),
    title: c,
    'aria-pressed': String(c === cur),
    onclick: () => pickChar(c)
  }, charPreview(c))))
  $('charPicker').showModal()
}
function pickChar (c) {
  markCharOption(c)
  chooseChar(pickingFor, c)
}
function markCharOption (c) {
  for (const o of $('charGrid').children) {
    const on = o.title === c
    o.classList.toggle('on', on)
    o.setAttribute('aria-pressed', String(on))
  }
}
$('charReset').addEventListener('click', () => {
  chooseChar(pickingFor, '')
  markCharOption(charFor(pickingFor))
})


// ---------- actions ----------
function confirmBox (title, body, text, okLabel) {
  return new Promise(resolve => {
    const d = $('confirm')
    $('confirmTitle').textContent = title
    $('confirmBody').textContent = body
    $('confirmText').textContent = text || ''
    $('confirmText').hidden = !text
    $('confirmOk').textContent = okLabel || tr('ยืนยัน')
    d.returnValue = ''
    d.addEventListener('close', () => resolve(d.returnValue === 'ok'), { once: true })
    d.showModal()
  })
}

// The banner is part of the page, so an open dialog covers it: a refused save
// inside one looked like nothing happened. The same text is also shown in the
// browser's top layer, above any dialog, and goes away by itself.
let toastTimer = 0
function toast (msg, bad) {
  const b = $('banner')
  b.hidden = false
  b.className = bad ? 'banner bad' : 'banner'
  b.textContent = msg
  const t = $('topToast')
  t.className = 'topToast' + (bad ? ' bad' : '')
  t.setAttribute('role', bad ? 'alert' : 'status')
  t.textContent = (bad ? '⚠ ' : '✓ ') + msg
  try {
    if (t.matches(':popover-open')) t.hidePopover()
    t.showPopover() // shown again each time, so it is above a dialog opened since
    clearTimeout(toastTimer)
    toastTimer = setTimeout(() => { if (t.matches(':popover-open')) t.hidePopover() }, bad ? 9000 : 4000)
  } catch (e) { /* older browser: the banner only */ }
}

async function act (path, body, okMsg) {
  try {
    let res
    try {
      res = await api(path, { method: 'POST', body })
    } catch (e) {
      // the service refused the same words to the same place a moment ago:
      // say so, and send only if the user means it (sendonce.go)
      if (e.code !== 'AIAGENT_DUPLICATE_SEND' || !body || body.resend) throw e
      if (!await confirmBox(tr('ข้อความนี้เพิ่งส่งไป'), e.message, body.message || body.task || body.answer || body.text || '', tr('ส่งซ้ำ'))) {
        toast(tr('ไม่ได้ส่งซ้ำ — ข้อความเดิมส่งไปแล้ว'), true)
        return false
      }
      res = await api(path, { method: 'POST', body: Object.assign({}, body, { resend: true }) })
    }
    const lost = !!res && res.delivery === 'unknown'
    const again = res && res.replayed ? tr(' (ส่งไปแล้วก่อนหน้านี้ ไม่ได้พิมพ์ซ้ำ)') : ''
    toast(okMsg(res || {}) + again + (lost ? tr(' — ยังไม่ยืนยันว่าถึง agent (ไม่พบในบทสนทนา) ตรวจก่อนส่งซ้ำ') : ''), lost)
    poll()
    return true
  } catch (e) {
    if (e.message !== 'auth') toast(tr('ส่งไม่สำเร็จ: ') + e.message, true)
    return false
  }
}

// ----- one send at a time -----
// A send waits seconds for the agent's transcript. The Send button used to
// work meanwhile, and a long message reached an agent twice in the same minute
// (owner, 2026-11-25). From the press until the service answers, the composer
// is locked: the button is disabled and says so, Enter does nothing, and the
// draft stays where it is until the send has succeeded.
const sendingIn = new WeakSet()
async function sendOnce (ta, btn, fn) {
  if (sendingIn.has(ta)) return false
  sendingIn.add(ta)
  const label = btn ? btn.textContent : ''
  ta.readOnly = true
  ta.setAttribute('aria-busy', 'true')
  if (btn) { btn.disabled = true; btn.classList.add('sending'); btn.textContent = tr('กำลังส่ง…') }
  try {
    return await fn()
  } finally {
    sendingIn.delete(ta)
    ta.readOnly = false
    ta.removeAttribute('aria-busy')
    if (btn) { btn.disabled = false; btn.classList.remove('sending'); btn.textContent = label }
    if (ta === $('askText')) updateComposerHint()
  }
}
// The same for a button that answers rather than a composer: one at a time
// per key (the session or run being answered).
const sendingKeys = new Set()
async function onceFor (key, fn) {
  if (sendingKeys.has(key)) return false
  sendingKeys.add(key)
  try { return await fn() } finally { sendingKeys.delete(key) }
}
// Each draft gets a random id the first time it is sent, and keeps it while it
// is the same draft: sending it again after a failure or a lost answer is then
// answered by the service with the first result instead of typing it twice.
function newSendId () {
  if (crypto.randomUUID) return crypto.randomUUID()
  return [...crypto.getRandomValues(new Uint8Array(16))].map(b => b.toString(16).padStart(2, '0')).join('')
}
const draftIds = new WeakMap()
function draftSendId (ta, key) {
  const d = draftIds.get(ta)
  if (d && d.key === key) return d.id
  const id = newSendId()
  draftIds.set(ta, { key, id })
  return id
}
function forgetSendId (ta) { draftIds.delete(ta) }

function focusComposer (ws, mode) {
  st.target = ws
  st.blockReason = ''
  $('askWs').value = ws
  updateComposerHint()
  $('askMode').value = mode
  $('askText').focus()
}

$('askWs').addEventListener('change', () => { st.target = $('askWs').value; st.blockReason = ''; updateComposerHint() })
// Pressing Send must not take the focus from the box. With the box focused the
// bar is taller (growText's floor); losing focus on mousedown collapsed it at
// once, the button moved away from under the pointer, the mouseup landed on
// the bar and the press did nothing — so people pressed again.
$('askGo').addEventListener('mousedown', e => e.preventDefault())
$('ask').addEventListener('submit', e => {
  e.preventDefault()
  sendOnce($('askText'), $('askGo'), sendFromComposer)
})
async function sendFromComposer () {
  const sess = composerSession()
  const ta = $('askText')
  if (sess) return sendChat(sess, ta, $('askThumbs'))
  const ws = $('askWs').value
  const picked = targetSession(ws)
  if (picked) return sendChat(picked, ta, $('askThumbs'))
  if (ws === 'pm') return sendToPM(ta)
  const mode = $('askMode').value
  let text = expandPastes(ta.value).trim()
  const files = st.attach || []
  if (!ws || (!text && !files.length)) return
  const prov = ws.startsWith('provider:') ? ws.slice('provider:'.length) : ''
  if (prov && mode !== 'mission') { toast(tr('ส่งตาม provider ได้เฉพาะงานใหม่ (mission)'), true); return }
  const w = st.snap && st.snap.workspaces.find(x => x.name === ws)
  const where = prov ? (prov === 'auto' ? tr('agent ที่เหลือ quota มากกว่า') : prov) : ws + (w ? ' (tmux ' + w.tmuxSession + ', ' + w.agentCommand + ')' : '')
  const ok = await confirmBox(
    mode === 'mission' ? tr('สร้างงานใหม่') : tr('ส่งข้อความถึง agent'),
    tr('ข้อความนี้จะถูกพิมพ์เข้า agent ใน ') + where + (mode === 'mission' ? tr(' และติดตามเป็นงาน agcmd eng') : ''),
    text + (files.length ? tr('\n\n+ รูปแนบ ') + files.length + tr(' รูป') : ''),
    tr('ส่งเลย')
  )
  if (!ok) return
  if (files.some(f => f.busy || !f.path)) { toast(tr('รูปยังอัปโหลดไม่เสร็จ — รออีกครู่แล้วกดส่งอีกที'), true); return }
  if (files.length) {
    text = (text || tr('ดูไฟล์ที่แนบ')) + '\n\n' + attachLines(files)
  }
  const sendId = draftSendId(ta, mode + '\n' + ws + '\n' + text)
  const sent = mode === 'mission'
    ? await act('/missions', prov ? { provider: prov, task: text, sendId } : { workspace: ws, task: text, sendId }, r => tr('สร้างงาน ') + (r.runId || '') + tr(' ใน ') + (r.workspace || ws) + tr(' แล้ว'))
    : await act('/messages', { workspace: ws, message: text, sendId }, () => tr('ส่งข้อความถึง ') + ws + tr(' แล้ว'))
  if (sent) {
    forgetSendId(ta)
    ta.value = ''
    clearPastes()
    clearDraft()
    for (const f of files) { if (f.url) URL.revokeObjectURL(f.url) }
    st.attach = []
    drawThumbs($('askThumbs'))
    growText()
  }
}

// Pictures are uploaded the moment they are picked, not when Send is pressed.
// On an iPad the photo library is a system screen, and coming back from it can
// reload a standalone (home-screen) app from scratch — which used to take the
// picture and the half-typed command with it. Once the bytes are on the service
// all that has to survive the reload is a path, which the draft below keeps.
async function addFiles (list, thumbs) {
  const el = thumbs || $('askThumbs')
  let skipped = 0
  const fresh = []
  for (const f of list) {
    if (!f) continue
    const zip = isZipFile(f)
    if (!zip && !/^image\/(png|jpeg|gif|webp)$/.test(f.type)) { skipped++; continue }
    if (zip && f.size > maxZipBytes) { skipped++; continue }
    if ((st.attach || []).length + fresh.length >= 10) { skipped++; continue }
    fresh.push(zip
      ? { blob: f, url: '', path: '', busy: true, kind: 'zip', name: f.name || 'zip' }
      : { blob: f, url: URL.createObjectURL(f), path: '', busy: true })
  }
  // A photo library can hand back HEIC, which the service does not take. Saying
  // nothing left the picture simply missing with no reason given.
  if (skipped) toast(tr('แนบไม่ได้ ') + skipped + tr(' ไฟล์ — รับเฉพาะ PNG, JPEG, GIF, WebP และ zip (ไม่เกิน 50 MB) รวมไม่เกิน 10 ไฟล์'), true)
  if (!fresh.length) return drawThumbs(el)
  st.attach = (st.attach || []).concat(fresh)
  drawThumbs(el)
  for (const a of fresh) {
    try {
      a.path = await uploadFile(a.blob, a.kind === 'zip')
    } catch (e) {
      const i = st.attach.indexOf(a)
      if (i >= 0) st.attach.splice(i, 1)
      if (a.url) URL.revokeObjectURL(a.url)
      toast(tr('อัปโหลดไม่สำเร็จ: ') + e.message, true)
    }
    a.busy = false
    drawThumbs(el)
    saveDraft()
  }
}

// What is in the composer, kept where a reload cannot reach it. Only the text
// and the uploaded paths are stored — never the image bytes.
const draftKey = 'aiagentDraft'
function saveDraft () {
  const text = $('askText') ? $('askText').value : ''
  const paths = (st.attach || []).map(a => a.path).filter(Boolean)
  // a folded paste is only a token in the box; without its text a reload
  // turned the draft into a message that says "[Pasted text #1 +40 lines]"
  const pastes = {}
  for (const tok in st.pastes || {}) if (text.includes(tok)) pastes[tok] = st.pastes[tok]
  try {
    if (!text && !paths.length) localStorage.removeItem(draftKey)
    else localStorage.setItem(draftKey, JSON.stringify({ text, paths, pastes, pasteSeq: st.pasteSeq || 0 }))
  } catch (e) { /* storage unavailable: the draft is simply not kept */ }
}
function clearDraft () { try { localStorage.removeItem(draftKey) } catch (e) { /* see above */ } }
function restoreDraft () {
  let d = null
  try { d = JSON.parse(localStorage.getItem(draftKey) || 'null') } catch (e) { d = null }
  if (!d) return
  if (d.text && $('askText') && !$('askText').value) {
    st.pastes = d.pastes || {}
    st.pasteSeq = d.pasteSeq || 0
    // a draft kept before pastes were: a token whose text is gone says nothing
    $('askText').value = d.text.replace(/\[Pasted text #\d+ \+\d+ lines\]/g, t => t in st.pastes ? t : '')
    growText()
  }
  for (const p of (d.paths || []).slice(0, 10)) {
    const a = { blob: null, url: '', path: p, busy: false }
    ;(st.attach = st.attach || []).push(a)
    // a stored zip is never served back, and there is nothing to preview
    if (isZipPath(p)) { a.kind = 'zip'; a.name = 'zip'; continue }
    loadUpload(p.split('/').pop()).then(u => { a.url = u; drawThumbs(st.thumbsEl || $('askThumbs')) }, () => {})
  }
  if (st.attach && st.attach.length) drawThumbs($('askThumbs'))
}
function growText () {
  const ta = $('askText')
  const keep = ta.scrollTop
  const atEnd = ta.selectionEnd >= ta.value.length - 1
  ta.style.height = 'auto'
  // While you are actually typing the box opens up to a few lines, so a command
  // long enough to be worth checking can be read before it is sent. It falls
  // back to the content height on blur.
  const floor = document.activeElement === ta ? 124 : 0
  ta.style.height = Math.min(240, Math.max(floor, ta.scrollHeight + 2)) + 'px'
  // The textarea is absolute and anchored to the bottom of a 44px wrapper, so
  // growing it used to send it up over the scene. Grow the wrapper with it:
  // the bar gets taller, the stage gets shorter, and nothing is covered.
  ta.parentElement.style.height = ta.style.height
  ta.scrollTop = atEnd ? ta.scrollHeight : keep
  const multi = ta.value.includes('\n') || ta.scrollHeight > 52
  ta.classList.toggle('multi', multi)
  const n = ta.value ? ta.value.split('\n').length : 0
  if (multi) ta.parentElement.dataset.lines = (n > 1 ? n + tr(' บรรทัด') : tr('ยาว')) + ' ↕'
  else delete ta.parentElement.dataset.lines
  // The bar just changed height, so the scene has less room than it had.
  // Refit it, or the board keeps its old scale and its bottom edge sits
  // under the composer.
  applyZoom()
}
// Typing mode: while the composer has focus the scene steps aside and the
// conversation takes the window. On an iPad the keyboard already covers half
// the screen, and what was left of it was a picture of a village with four
// lines of chat squeezed down the side.
function setTyping (on) {
  document.body.classList.toggle('typing', on)
  growText()
  requestAnimationFrame(applyZoom)
}
$('askText').addEventListener('focus', () => setTyping(true))
// Leaving the box does NOT end typing mode. It used to, and pressing on a
// message to drag-select it for copying took the focus, so the window shrank
// back under the pointer and the selection never happened. The box still
// settles to its content height; the ✕ (or changing view) brings the scene back.
$('askText').addEventListener('blur', () => setTimeout(growText, 150))
$('typingClose').addEventListener('click', () => {
  $('askText').blur()
  setTyping(false)
})
$('askText').addEventListener('input', () => { growText(); saveDraft() })
$('askText').addEventListener('keydown', e => {
  if (e.key === 'Tab' && !e.shiftKey && (st.suggest || []).length && composerSession()) {
    const v = st.suggest[0]
    const cur = $('askText').value
    if (!cur.trim() || v.startsWith(cur)) { e.preventDefault(); useSuggest(v); return }
  }
  // a held-down Enter repeats; only the first press sends
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); if (!e.repeat) $('ask').requestSubmit() }
})
$('askText').addEventListener('paste', e => {
  const files = [...(e.clipboardData ? e.clipboardData.items : [])].filter(i => i.kind === 'file').map(i => i.getAsFile())
  if (files.length) { e.preventDefault(); addFiles(files); return }
  // long pasted text collapses into a chip-like token, as in the CLI; the
  // full text is put back when the message is sent
  const text = e.clipboardData ? e.clipboardData.getData('text/plain') : ''
  const lines = text.split('\n').length
  if (lines < 4 && text.length < 600) return
  e.preventDefault()
  st.pastes = st.pastes || {}
  st.pasteSeq = (st.pasteSeq || 0) + 1
  const tok = '[Pasted text #' + st.pasteSeq + ' +' + lines + ' lines]'
  st.pastes[tok] = text
  const ta = $('askText')
  ta.setRangeText(tok, ta.selectionStart, ta.selectionEnd, 'end')
  growText()
  saveDraft()
})

// expandPastes swaps collapsed paste tokens back to their text
function expandPastes (text) {
  for (const tok in st.pastes || {}) text = text.split(tok).join(st.pastes[tok])
  return text
}
function clearPastes () { st.pastes = {} }
$('askClip').addEventListener('click', () => $('askFile').click())
$('askFile').addEventListener('change', () => { addFiles($('askFile').files); $('askFile').value = '' })
$('ask').addEventListener('dragover', e => { e.preventDefault(); $('ask').classList.add('drop') })
$('ask').addEventListener('dragleave', () => $('ask').classList.remove('drop'))
$('ask').addEventListener('drop', e => { e.preventDefault(); $('ask').classList.remove('drop'); addFiles(e.dataTransfer.files) })

function fitLightbox () {
  const img = $('lightboxImg')
  if (!img.naturalWidth) return
  const k = Math.min(innerWidth * 0.92 / img.naturalWidth, innerHeight * 0.88 / img.naturalHeight)
  img.style.width = Math.round(img.naturalWidth * k) + 'px'
  img.style.height = Math.round(img.naturalHeight * k) + 'px'
}
$('lightboxImg').addEventListener('load', fitLightbox)
addEventListener('resize', () => { if ($('lightbox').open) fitLightbox() })
function openLightbox (src) {
  $('lightboxImg').src = src
  $('lightboxImg').hidden = false
  $('lightbox').showModal()
}
$('lightbox').addEventListener('click', e => { if (e.target === $('lightbox') || e.target === $('lightboxImg')) $('lightbox').close() })

function doAnswer (run, answer) {
  answer = (answer || '').trim()
  if (!answer) return
  return onceFor('run:' + run.id, async () => {
    if (!await confirmBox(tr('ตอบ ') + run.id, tr('คำตอบจะถูกพิมพ์เข้า agent ใน ') + run.tmuxSession, answer, tr('ส่งคำตอบ'))) return
    await act('/runs/' + encodeURIComponent(run.id) + '/answer', { answer, sendId: newSendId() }, () => tr('ส่งคำตอบให้ ') + run.id + tr(' แล้ว'))
  })
}

async function doStop (sess) {
  const short = sess.sessionId.slice(0, 8)
  const how = sess.stopVia === 'claudeStop'
    ? tr('จะรัน claude stop ') + short + tr(' — หยุด background session นี้ บทสนทนายังเก็บไว้ เปิดต่อได้ด้วย claude attach ') + short
    : tr('จะรัน agcmd restart ') + sess.dispatchVia + tr(' — ส่ง Escape ขัดงานรอบนี้ โปรแกรม agent ยังเปิดอยู่และสั่งต่อได้')
  if (!await confirmBox(tr('หยุด ') + sess.displayName + '?', how, sess.title || '', tr('หยุด agent'))) return
  act('/sessions/' + encodeURIComponent(sess.sessionId) + '/stop', null, r => tr('หยุด ') + sess.displayName + tr(' แล้ว (') + (r.via || '') + ')')
}

async function doResume (sess) {
  if (!await confirmBox(tr('เปิดต่อ') + ' ' + sess.displayName + '?', tr('จะเปิดบทสนทนานี้ต่อด้วย claude --resume ในโฟลเดอร์เดิม โดยไม่พิมพ์ข้อความใดเข้าไป'), sess.cwd || '', tr('เปิดต่อ'))) return
  act('/sessions/' + encodeURIComponent(sess.sessionId) + '/resume', null, r => (r.state === 'running' ? tr('ทำงานอยู่แล้ว') : tr('เปิดต่อแล้ว')) + ' · ' + sess.displayName)
}

async function doRunStatus (run, action) {
  const label = action === 'approve' ? tr('อนุมัติ') : tr('ยกเลิก')
  if (!await confirmBox(label + ' ' + run.id, tr('เปลี่ยนสถานะงานใน agcmd เป็น ') + (action === 'approve' ? 'READY' : 'ABORTED') + tr(' (ไม่ปิด process ของ agent)'), run.task.slice(0, 300), label)) return
  act('/runs/' + encodeURIComponent(run.id) + '/' + action, null, () => label + ' ' + run.id + tr(' แล้ว'))
}

// ---------- login / views ----------
function showLogin (msg) {
  clearTimeout(st.timer)
  const d = $('login')
  $('loginErr').textContent = msg || ''
  const petals = $('loginPetals')
  if (!petals.childElementCount) {
    for (let i = 0; i < 14; i++) {
      const p = h('i')
      p.style.left = Math.round(i * 7 + 3) + '%'
      p.style.animationDelay = (i * 0.9).toFixed(1) + 's'
      p.style.animationDuration = (10 + (i % 5) * 1.6).toFixed(1) + 's'
      petals.append(p)
    }
  }
  if (!d.open) {
    d.showModal()
    // the token form is the default; a QR is made only when asked for
    $('loginQR').hidden = true
    $('loginQRAsk').hidden = false
  }
  $('loginToken').focus()
}
function signIn (t) {
  st.loginQRSeq = (st.loginQRSeq || 0) + 1 // any QR still polling stops
  clearTimeout(st.loginQRTimer)
  st.token = t
  safeSet(roleKey, '')
  showMenu('')
  safeSet(tokenKey, t, $('loginRemember') && $('loginRemember').checked)
  $('loginToken').value = ''
  $('login').close()
  poll()
}
$('loginQRAsk').addEventListener('click', () => {
  $('loginQRAsk').hidden = true
  startLoginQR()
})
$('loginForm').addEventListener('submit', e => {
  e.preventDefault()
  const t = $('loginToken').value.trim()
  if (!t) return
  signIn(t)
})
// QR login (loginqr.go): the page asks for a one-time request, shows its QR,
// and polls with the nonce the server gave it. The nonce lives only in this
// closure: never in the QR, the URL or storage, so a photo of the QR cannot
// collect the token. The request lasts two minutes and opens once.
async function startLoginQR () {
  clearTimeout(st.loginQRTimer)
  const seq = st.loginQRSeq = (st.loginQRSeq || 0) + 1
  const box = $('loginQR')
  const img = $('loginQRImg')
  const left = $('loginQRLeft')
  const again = $('loginQRNew')
  let r
  try {
    const res = await fetch('/aiagent/login-qr', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ origin: location.origin }), cache: 'no-store' })
    const body = await res.json().catch(() => null)
    if (!res.ok || !body || body.code !== 'SUCCESS') throw new Error('login-qr ' + res.status)
    r = body.details
  } catch (e) {
    // no QR on this address (plain http, an older server, too many asks): the token form still works
    if (seq === st.loginQRSeq) box.hidden = true
    return
  }
  if (seq !== st.loginQRSeq || !$('login').open) return
  // never count past two minutes, even if this computer's clock runs behind
  const ends = Math.min(Date.parse(r.expiresAt), Date.now() + 2 * 60 * 1000)
  const over = text => {
    img.classList.add('expired')
    left.textContent = text
    again.hidden = false
  }
  img.src = r.qr
  img.classList.remove('expired')
  again.hidden = true
  box.hidden = false
  const step = async () => {
    if (seq !== st.loginQRSeq || !$('login').open) return
    const sec = Math.max(0, Math.round((ends - Date.now()) / 1000))
    if (!sec) { over(tr('QR นี้หมดอายุแล้ว กด QR ใหม่')); return }
    left.textContent = tr('ใช้ได้อีก ') + Math.floor(sec / 60) + ':' + String(sec % 60).padStart(2, '0')
    let res = null
    let body = null
    try {
      res = await fetch('/aiagent/login-qr/' + encodeURIComponent(r.id), { headers: { 'X-Aiagent-Login-Nonce': r.nonce }, cache: 'no-store' })
      body = await res.json().catch(() => null)
    } catch (e) { res = null }
    if (seq !== st.loginQRSeq || !$('login').open) return
    const d = body && body.details
    if (res && res.ok && d && d.status === 'approved' && d.token) { signIn(d.token); return }
    if (res && res.status === 404) { over(tr('QR นี้ใช้ไม่ได้แล้ว กด QR ใหม่')); return }
    st.loginQRTimer = setTimeout(step, 2000)
  }
  step()
}
$('loginQRNew').addEventListener('click', () => startLoginQR())
$('login').addEventListener('cancel', e => e.preventDefault())
// One row per view: its nav button, the name kept in localStorage, its pane.
// web/assets_test.go checks every row against index.html.
const views = [
  ['viewMap', 'map', 'village'],
  ['viewList', 'list', 'list'],
  ['viewPM', 'pm', 'pm'],
  ['viewTasks', 'tasks', 'tasks'],
  ['viewReview', 'review', 'review']
]
function setView (view) {
  st.view = view
  for (const [id, v, pane] of views) {
    $(id).classList.toggle('on', view === v)
    $(id).setAttribute('aria-pressed', String(view === v))
    // the village is not hidden but switched off, so it keeps its size
    if (v !== 'map') $(pane).hidden = view !== v
  }
  $('village').classList.toggle('off', view !== 'map')
  $('sceneTools').hidden = view !== 'map'
  // The stage carries the time-of-day sky and the village heading. Neither
  // belongs behind a table, so the list and task views say so on the body
  // and the stylesheet turns them off from there.
  document.body.classList.toggle('listView', view !== 'map')
  // The PM and review pages bring their own side column, so the detail panel
  // steps aside and the page gets the whole width.
  document.body.classList.toggle('fullView', view === 'pm' || view === 'review')
  if (view === 'pm') {
    // The composer follows the page: here it talks to the PM, not to whoever
    // was selected in the village.
    stopFollow()
    st.sel = null
    st.chat = null
    st.inspKey = ''
    st.blockReason = ''
    st.target = 'pm'
    st.pmStick = true
  }
  if (st.snap) render()
}
$('stageBack').addEventListener('click', () => goBack())

// On a phone the village opens as the list, not the map. The board is 2060px
// wide and lands near 19% on a 390px screen — a picture of a village rather
// than something you can read. The map is still one tap away; this only
// chooses what you land on, and only the first time, so a deliberate switch
// sticks.
// The list is home on every screen now; the map is one tap away. A deliberate
// switch still sticks, so someone who picks the map keeps landing on it.
try {
  const saved = localStorage.getItem('aiagentView')
  const role = st.token ? safeGet(roleKey) : ''
  showMenu(role)
  const adminOnly = saved === 'pm' || saved === 'tasks'
  setView(views.some(row => row[1] === saved) && (role === 'admin' || !adminOnly) ? saved : 'list')
} catch (e) { setView('list') }
for (const [id, v] of views) $(id).addEventListener('click', () => goView(v))
// ----- follow the selected agent from room to room -----
function followActor () {
  const id = st.follow
  if (!id) return null
  const sess = st.snap.sessions.find(x => x.sessionId === id.split(':')[0])
  if (!sess || !sess.live) return null
  const a = actorsFor().find(x => x.id === id)
  return a ? Object.assign({ house: HOUSE }, a) : null
}
function stopFollow () {
  st.follow = null
  clearTimeout(st.followTimer)
  st.followTimer = null
  $('followChip').hidden = true
}
function followTick () {
  const chip = $('followChip')
  const a = followActor()
  if (!a || st.view !== 'map' || !rooms2[a.room]) { chip.hidden = true; return }
  chip.hidden = false
  chip.textContent = tr('📍 ติดตาม ') + (a.name.length > 22 ? a.name.slice(0, 21) + '…' : a.name) + ' · ' + (roomTitle[a.room] || '') + '  ✕'
  if (st.house === a.house && st.roomFocus === a.room) { clearTimeout(st.followTimer); st.followTimer = null; return }
  if (st.followTimer) return
  // first visit jumps straight in; later moves wait so the walk out is seen
  const delay = st.house === a.house && st.roomFocus ? 1400 : 0
  st.followTimer = setTimeout(() => {
    st.followTimer = null
    const b = followActor()
    if (!b || !rooms2[b.room]) return
    st.house = b.house
    openRoom(b.room, true)
  }, delay)
}
$('followChip').addEventListener('click', stopFollow)

function goBack () {
  stopFollow()
  if (st.roomFocus) st.roomFocus = null
  else st.house = null
  st.drawn = ''
  render()
}
$('zoomIn').addEventListener('click', () => { st.zoom = Math.min(3, zoomLevel() + 0.2); applyZoom() })
$('zoomOut').addEventListener('click', () => { st.zoom = Math.max(0.6, zoomLevel() - 0.2); applyZoom() })
$('fullBtn').addEventListener('click', () => {
  // The body, not the stage: fullscreening the stage alone left the composer
  // outside it, so there was no way to answer without leaving fullscreen
  // first. The header hides itself in fullscreen (see styles.css) so the
  // scene still gets nearly all of the extra room.
  try {
    if (document.fullscreenElement) document.exitFullscreen()
    else document.body.requestFullscreen()
  } catch (e) { /* fullscreen not allowed */ }
})
window.addEventListener('resize', () => applyZoom())
try { const ro = new ResizeObserver(() => applyZoom()); ro.observe($('village')); ro.observe(document.querySelector('.stageHead')) } catch (e) { /* older browser: window resize only */ }
// The panel element itself lives in the sheet while it is open, so it has to
// be put back when the sheet closes — including on Esc and on the backdrop.
$('panelPop').addEventListener('close', closePanelSheet)
document.addEventListener('fullscreenchange', () => setTimeout(applyZoom, 50))
document.addEventListener('keydown', e => {
  if (e.key !== 'Escape' || document.querySelector('dialog[open]')) return
  if (st.sel) back()
  else if (st.house) goBack()
})
setInterval(renderConn, 2000)
// a standalone app on iOS can be reloaded out from under the composer
restoreDraft()
poll()

// ---------- markdown ----------
//
// Agents write markdown; the terminal renders it and the web used to show the
// raw characters. This turns it into DOM nodes.
//
// Built with createElement/createTextNode only — never innerHTML. The text is
// written by an agent, which may be relaying something it read on the
// internet, so it is untrusted input; building nodes means a stray "<script>"
// is characters on the screen and can never be anything else. It also keeps
// the strict CSP intact.

const MD_LINK = /\[([^\]\n]+)\]\(([^)\s]+)\)/g
const MD_CODE = /`([^`\n]+)`/g
const MD_BOLD = /\*\*([^*\n]+)\*\*/g
const MD_ITAL = /(^|[\s(])[*_]([^*_\n]+)[*_](?=[\s).,!?:;]|$)/g
const MD_STRIKE = /~~([^~\n]+)~~/g

// A private-use character as the placeholder: it cannot occur in text an
// agent writes, so a message containing the literal digits of a slot index
// can never be mistaken for one.
const MD_SLOT = '\uE000'

// mdInline renders one line's inline marks into a fragment.
function mdInline (text) {
  const frag = document.createDocumentFragment()
  const slots = []
  const hold = node => {
    slots.push(node)
    return MD_SLOT + (slots.length - 1) + MD_SLOT
  }
  // Code spans come out first and go back as nodes, so markers inside them —
  // a ** in a shell command, say — are never treated as formatting.
  let s = String(text).replace(MD_CODE, (_, code) => hold(h('code', null, code)))
  s = s.replace(MD_LINK, (whole, label, url) =>
    // Only real web links become links. Anything else (javascript:, data:, a
    // bare path) stays as the characters the agent wrote.
    /^https?:\/\//i.test(url) ? hold(h('a', { href: url, target: '_blank', rel: 'noopener noreferrer' }, label)) : whole)
  s = s.replace(MD_BOLD, (_, t) => hold(h('strong', null, t)))
  s = s.replace(MD_STRIKE, (_, t) => hold(h('del', null, t)))
  s = s.replace(MD_ITAL, (_, lead, t) => lead + hold(h('em', null, t)))

  for (const piece of s.split(MD_SLOT)) {
    if (piece === '') continue
    if (/^\d+$/.test(piece) && slots[Number(piece)]) frag.append(slots[Number(piece)])
    else frag.append(document.createTextNode(piece))
  }
  return frag
}

function mdTableRow (line) {
  return line.replace(/^\s*\|/, '').replace(/\|\s*$/, '').split('|').map(c => c.trim())
}

// renderMd turns markdown into a fragment of block elements.
function renderMd (text) {
  const frag = document.createDocumentFragment()
  const lines = String(text == null ? '' : text).replace(/\r\n/g, '\n').split('\n')
  let i = 0
  let para = []
  const flush = () => {
    if (!para.length) return
    const p = h('p', { class: 'mdP' })
    para.forEach((l, n) => { if (n) p.append(h('br')); p.append(mdInline(l)) })
    frag.append(p)
    para = []
  }

  while (i < lines.length) {
    const line = lines[i]

    // Fenced code is kept verbatim: no inline parsing inside, because the
    // whole point of a code block is that the characters are the content.
    const fence = /^\s*```(\S*)\s*$/.exec(line)
    if (fence) {
      flush()
      const lang = fence[1]
      const body = []
      i++
      while (i < lines.length && !/^\s*```\s*$/.test(lines[i])) body.push(lines[i++])
      i++ // closing fence
      const pre = h('pre', { class: 'mdPre' }, h('code', null, body.join('\n')))
      if (lang) pre.setAttribute('data-lang', lang)
      frag.append(pre)
      continue
    }

    if (/^\s*(---+|\*\*\*+|___+)\s*$/.test(line)) { flush(); frag.append(h('hr', { class: 'mdHr' })); i++; continue }

    const head = /^(#{1,6})\s+(.*)$/.exec(line)
    if (head) {
      flush()
      // Never above h3: these land inside a side panel, and an agent's "#"
      // should not outrank the panel's own heading.
      const tag = 'h' + Math.min(6, head[1].length + 2)
      frag.append(h(tag, { class: 'mdH' }, mdInline(head[2])))
      i++
      continue
    }

    // Table: a header row followed by a |---|---| separator.
    if (/^\s*\|.*\|\s*$/.test(line) && i + 1 < lines.length && /^\s*\|[\s:|-]+\|\s*$/.test(lines[i + 1])) {
      flush()
      const table = h('table', { class: 'mdTable' })
      table.append(h('thead', null, h('tr', null, ...mdTableRow(line).map(c => h('th', null, mdInline(c))))))
      i += 2
      const tbody = h('tbody')
      while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) {
        tbody.append(h('tr', null, ...mdTableRow(lines[i]).map(c => h('td', null, mdInline(c)))))
        i++
      }
      table.append(tbody)
      frag.append(table)
      continue
    }

    const bullet = /^\s*[-*+]\s+(.*)$/.exec(line)
    const numbered = /^\s*\d+[.)]\s+(.*)$/.exec(line)
    if (bullet || numbered) {
      flush()
      const ordered = !!numbered
      const list = h(ordered ? 'ol' : 'ul', { class: 'mdList' })
      while (i < lines.length) {
        const m = ordered ? /^\s*\d+[.)]\s+(.*)$/.exec(lines[i]) : /^\s*[-*+]\s+(.*)$/.exec(lines[i])
        if (!m) break
        list.append(h('li', null, mdInline(m[1])))
        i++
      }
      frag.append(list)
      continue
    }

    const quote = /^\s*>\s?(.*)$/.exec(line)
    if (quote) {
      flush()
      const box = h('blockquote', { class: 'mdQuote' })
      while (i < lines.length) {
        const m = /^\s*>\s?(.*)$/.exec(lines[i])
        if (!m) break
        if (box.childNodes.length) box.append(h('br'))
        box.append(mdInline(m[1]))
        i++
      }
      frag.append(box)
      continue
    }

    if (line.trim() === '') { flush(); i++; continue }
    para.push(line)
    i++
  }
  flush()
  return frag
}

// mdBody is the element a message's text goes in.
function mdBody (text, cls) {
  const box = h('div', { class: 'md' + (cls ? ' ' + cls : '') })
  box.append(renderMd(text))
  return box
}

// ---------- reading room: bigger, wider, resizable ----------
//
// Agent output is long. The terminal gives it the whole width; the web used to
// give it a fixed 380px column and a small dialog. These let the reader take
// the space the text needs: a full-screen toggle and a drag corner on every
// dialog, a drag edge on the side panel, and a way to throw the panel up as a
// full-size sheet.
//
// Sizes are remembered in localStorage, per browser, because a reading width
// is a preference and re-dragging it on every visit is the annoyance this is
// meant to remove.

const UI_PREF = 'aiagentUi'

function uiPrefs () {
  try { return JSON.parse(localStorage.getItem(UI_PREF) || '{}') } catch (e) { return {} }
}
function uiPref (key, value) {
  const p = uiPrefs()
  if (value === undefined) return p[key]
  p[key] = value
  try { localStorage.setItem(UI_PREF, JSON.stringify(p)) } catch (e) { /* private mode: live without it */ }
}

// enhanceDialogs adds a full-screen toggle to every dialog that has a header.
// Done once, in JS, rather than repeated in the markup, so a new dialog gets
// it for free.
function enhanceDialogs () {
  for (const dlg of document.querySelectorAll('dialog.screenPop')) {
    const head = dlg.querySelector('.spHead')
    if (!head || head.querySelector('.fullToggle')) continue
    const btn = h('button', {
      type: 'button',
      class: 'ghost fullToggle',
      'aria-pressed': 'false',
      title: tr('เต็มจอ (F)'),
      'aria-label': tr('สลับเต็มจอ'),
      onclick: () => toggleDialogFull(dlg)
    }, '⤢')
    head.insertBefore(btn, head.querySelector('form'))
    if (uiPref('dlgFull')) toggleDialogFull(dlg, true)
  }
}

function toggleDialogFull (dlg, force) {
  const on = force === undefined ? !dlg.classList.contains('full') : force
  dlg.classList.toggle('full', on)
  const btn = dlg.querySelector('.fullToggle')
  if (btn) {
    btn.setAttribute('aria-pressed', String(on))
    btn.textContent = on ? '⤡' : '⤢'
  }
  // A size dragged by hand would otherwise fight the full-screen rule.
  if (on) { dlg.style.width = ''; dlg.style.height = '' }
  uiPref('dlgFull', on)
}

// F toggles full screen while a dialog is open — but never while the operator
// is typing, or "f" would stop reaching the message box.
document.addEventListener('keydown', e => {
  if (e.key !== 'f' && e.key !== 'F') return
  if (e.metaKey || e.ctrlKey || e.altKey) return
  const t = e.target
  if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
  const open = document.querySelector('dialog.screenPop[open]')
  if (!open) return
  e.preventDefault()
  toggleDialogFull(open)
})

// ---------- side panel width ----------

const INSP_MIN = 320
const INSP_MAX_FRAC = 0.8

function applyInspWidth (px) {
  const max = Math.round(window.innerWidth * INSP_MAX_FRAC)
  const w = Math.max(INSP_MIN, Math.min(max, Math.round(px)))
  document.documentElement.style.setProperty('--inspW', w + 'px')
  return w
}

function initPanelResize () {
  const main = document.querySelector('.main')
  if (!main || main.querySelector('.panelGrip')) return
  const saved = uiPref('inspW')
  if (saved) applyInspWidth(saved)

  const grip = h('div', {
    class: 'panelGrip',
    role: 'separator',
    'aria-orientation': 'vertical',
    'aria-label': tr('ลากเพื่อปรับความกว้างแผงขวา'),
    tabindex: 0,
    title: tr('ลากเพื่อปรับความกว้าง · ดับเบิลคลิกเพื่อรีเซ็ต')
  })

  let dragging = false
  const widthFrom = clientX => window.innerWidth - clientX

  grip.addEventListener('pointerdown', e => {
    dragging = true
    grip.setPointerCapture(e.pointerId)
    document.body.classList.add('resizing')
    e.preventDefault()
  })
  grip.addEventListener('pointermove', e => {
    if (!dragging) return
    applyInspWidth(widthFrom(e.clientX))
  })
  const stop = e => {
    if (!dragging) return
    dragging = false
    document.body.classList.remove('resizing')
    try { grip.releasePointerCapture(e.pointerId) } catch (err) { /* already gone */ }
    uiPref('inspW', applyInspWidth(widthFrom(e.clientX)))
  }
  grip.addEventListener('pointerup', stop)
  grip.addEventListener('pointercancel', stop)
  grip.addEventListener('dblclick', () => { uiPref('inspW', applyInspWidth(380)) })
  // Keyboard: the same control, for anyone not using a pointer.
  grip.addEventListener('keydown', e => {
    const step = e.shiftKey ? 48 : 16
    const cur = parseInt(getComputedStyle(document.documentElement).getPropertyValue('--inspW')) || 380
    if (e.key === 'ArrowLeft') uiPref('inspW', applyInspWidth(cur + step))
    else if (e.key === 'ArrowRight') uiPref('inspW', applyInspWidth(cur - step))
    else return
    e.preventDefault()
  })
  main.append(grip)
}

// ---------- panel as a sheet ----------
//
// Moves the live panel element into a dialog rather than copying its contents,
// so everything inside keeps working: the chat keeps updating, buttons keep
// their handlers, and no state is duplicated.

function openPanelSheet () {
  const dlg = $('panelPop')
  const insp = $('insp')
  if (!dlg || !insp) return
  const body = $('panelPopBody')
  body.replaceChildren(insp)
  insp.classList.add('inSheet')
  dlg.showModal()
}

function closePanelSheet () {
  const insp = $('insp')
  const main = document.querySelector('.main')
  if (!insp || !main || !insp.classList.contains('inSheet')) return
  insp.classList.remove('inSheet')
  main.append(insp)
}

// renameSession renames in the CLI when it can reach the session, and keeps a
// local alias only when it cannot. There is no `claude rename` on the command
// line, so /rename typed into the session is the only way the two can agree —
// an alias always disagrees with one side or the other. Either way an empty
// answer clears the alias and shows the CLI's own name again.
async function renameSession (sess) {
  const current = sess.displayName || ''
  const next = window.prompt(
    tr('ตั้งชื่อ session นี้\n') +
    tr('ถ้า agent รับข้อความจากเว็บได้ จะส่ง /rename เข้าไปให้ ชื่อใน CLI เปลี่ยนตามด้วย\n') +
    tr('เว้นว่างเพื่อกลับไปใช้ชื่อเดิม'),
    current)
  if (next === null) return
  try {
    const r = await api('/sessions/' + encodeURIComponent(sess.sessionId) + '/name', { method: 'POST', body: { name: next } })
    st.inspKey = ''
    await poll()
    // Say which of the two happened. Saying nothing is what made the old
    // behaviour confusing: the name changed on screen and nowhere else.
    if (next.trim()) {
      toast(r && r.syncedToCli
        ? tr('เปลี่ยนชื่อใน CLI แล้ว — ถ้า agent กำลังทำงานอยู่ จะมีผลเมื่อรอบนี้จบ')
        : tr('เปลี่ยนเฉพาะในเว็บ — ส่งเข้า CLI ไม่ได้ (agent รับข้อความจากเว็บไม่ได้)'))
    }
  } catch (e) {
    if (e.message !== 'auth') toast(tr('เปลี่ยนชื่อไม่ได้: ') + e.message)
  }
}

// ---------- language ----------
// The button shows the language you would switch TO, so it reads as an action
// rather than a label. Everything else is in i18n.js: the dictionary, the
// stored choice, and the reload setLang does — the note there says why
// switching reloads rather than repaints.
function initLang () {
  const b = $('langBtn')
  if (!b) return
  b.textContent = currentLang() === 'en' ? 'ไทย' : 'EN'
  b.addEventListener('click', () => setLang(currentLang() === 'en' ? 'th' : 'en'))
}

// ---------- tasks: the PM's board ----------
// Everything here reads /api/tasks and acts through the user's own moves.
// Nothing shows a percentage: a task is in a state, on an attempt, with the
// evidence the orchestrator recorded — never an invented progress figure.
const taskStatusLabel = {
  queued: tr('รอคิว'),
  ready: tr('พร้อมเริ่ม'),
  running: tr('กำลังทำ'),
  verifying: tr('กำลังตรวจ'),
  awaitingReview: tr('รอคุณตรวจ'),
  completed: tr('เสร็จแล้ว'),
  blocked: tr('ติดขัด'),
  failed: tr('ล้มเหลว'),
  cancelled: tr('ยกเลิก')
}
const attemptLabel = {
  starting: tr('กำลังส่ง'),
  running: tr('agent ได้รับงานแล้ว'),
  unknown: tr('ไม่ทราบว่า agent ได้รับหรือยัง'),
  reported: tr('agent รายงานว่าเสร็จ'),
  failed: tr('ล้มเหลว'),
  cancelled: tr('ยกเลิก'),
  orphaned: tr('ขาดการติดต่อ')
}
const actorLabel = { user: tr('คุณ'), orchestrator: tr('ระบบ'), pm: 'PM', worker: 'agent' }
const boardCols = ['queued', 'ready', 'running', 'verifying', 'awaitingReview', 'completed']
const laneStatuses = ['blocked', 'failed', 'cancelled']
let tasksSeq = 0

// stalledAttempt is the running task's attempt once its lease has run out.
// The orchestrator renews the lease while the agent's transcript moves and
// requeues nothing by itself, so a session stopped from a terminal leaves its
// task "running" for good: this is what tells it from a task being worked on.
function stalledAttempt (t) {
  const a = t.attempts[t.attempts.length - 1]
  if (t.status !== 'running' || !a || !a.leaseExpiresAt) return null
  return new Date(a.leaseExpiresAt).getTime() < Date.now() ? a : null
}

// quietLine says only what is known. With a session, its transcript stopped
// moving then. An attempt confirmed by hand names no session, so nothing
// watches it and its lease runs out while the agent may be hard at work.
function quietLine (a) {
  if (!a.sessionId) return tr('ไม่รู้ว่า agent ยังทำอยู่หรือไม่ — งานนี้ไม่ได้ผูกกับ session')
  return tr('agent เงียบตั้งแต่ ') + ago(a.heartbeatAt) + tr(' — อาจหยุดไปแล้ว')
}

function taskPill (status) {
  return h('span', { class: 'tpill s-' + status }, taskStatusLabel[status] || status)
}

async function renderTasks () {
  const seq = ++tasksSeq
  const box = $('tasks')
  let tasks
  try {
    tasks = (await api('/tasks')).tasks
    st.taskDetailData = st.taskOpen ? await api('/tasks/' + encodeURIComponent(st.taskOpen)) : null
  } catch (e) {
    if (e.message === 'auth' || seq !== tasksSeq) return
    box.replaceChildren(h('div', { class: 'tasksEmpty' }, tr('ระบบงานยังไม่เปิดบนเครื่องนี้ — อัปเดต aiagent แล้ว restart service')))
    return
  }
  if (seq !== tasksSeq) return
  if (!st.taskForm) st.taskForm = taskForm()
  const newestFirst = [...tasks].sort((a, b) => (a.updatedAt < b.updatedAt ? 1 : -1))
  const filterKey = st.taskFilter || 'all'
  const matched = filterTasks(newestFirst, filterKey)
  const perPage = st.taskPerPage || 10
  const pg = pageTasks(matched, st.taskPage || 1, perPage)
  st.taskPage = pg.page
  const head = h('div', { class: 'tasksHead' },
    h('h2', {}, tr('กระดานงาน')),
    h('button', { type: 'button', class: 'pri', onclick: () => { st.taskFormOpen = !st.taskFormOpen; renderTasks() } },
      st.taskFormOpen ? tr('ปิดฟอร์ม') : tr('+ งานใหม่')))
  const filters = h('div', { class: 'tfilters' }, [['all', tr('ทั้งหมด')], ['active', tr('กำลังทำ')], ['done', tr('เสร็จแล้ว')], ['cancelled', tr('ยกเลิก')]].map(([k, label]) =>
    h('button', { type: 'button', class: 'tfilter' + (k === filterKey ? ' on' : ''), onclick: () => { st.taskFilter = k; st.taskPage = 1; renderTasks() } }, label, ' ', h('span', {}, String(filterTasks(newestFirst, k).length)))))
  const pager = h('div', { class: 'tpager' },
    h('button', { type: 'button', class: 'ghost', disabled: pg.page <= 1, onclick: () => { st.taskPage = pg.page - 1; renderTasks() } }, tr('ก่อนหน้า')),
    h('span', {}, tr('หน้า ') + pg.page + ' / ' + pg.pages),
    h('button', { type: 'button', class: 'ghost', disabled: pg.page >= pg.pages, onclick: () => { st.taskPage = pg.page + 1; renderTasks() } }, tr('ถัดไป')),
    h('label', {}, tr('ต่อหน้า '), h('select', { onchange: e => { st.taskPerPage = Number(e.target.value); st.taskPage = 1; renderTasks() } },
      [10, 20, 50].map(n => h('option', { value: n, selected: n === perPage }, String(n))))))
  const cols = boardCols.map(s => {
    const mine = pg.items.filter(t => t.status === s)
    return h('div', { class: 'tcol' },
      h('div', { class: 'tcolHead' }, taskPill(s), h('span', { class: 'tcount' }, String(matched.filter(t => t.status === s).length))),
      mine.length ? mine.map(taskCard) : h('div', { class: 'tnone' }, '—'))
  })
  const lane = pg.items.filter(t => laneStatuses.includes(t.status))
  // replaceChildren, unlike h(), would print a null as the text "null".
  box.replaceChildren(...[
    head,
    st.taskFormOpen ? st.taskForm : null,
    tasks.length ? null : h('div', { class: 'tasksEmpty' }, tr('ยังไม่มีงาน — กด + งานใหม่ เพื่อสร้างงานแรก')),
    tasks.length ? filters : null,
    tasks.length ? pager : null,
    tasks.length && !matched.length ? h('div', { class: 'tasksEmpty' }, tr('ไม่มีงานในตัวกรองนี้')) : null,
    h('div', { class: 'tboard' }, cols),
    lane.length ? h('div', { class: 'tlane' }, h('div', { class: 'tcolHead' }, tr('ติดขัด / ล้มเหลว / ยกเลิก')), h('div', { class: 'tlaneCards' }, lane.map(t => h('div', { class: 'tlaneItem' },
      taskCard(t),
      t.status === 'cancelled' ? h('button', { type: 'button', class: 'warn tdelBadge', onclick: () => deleteTask(t) }, tr('ลบงาน')) : null)))) : null,
    st.taskDetailData ? taskDetail(st.taskDetailData) : null
  ].filter(Boolean))
}

function filterTasks (list, key) {
  if (key === 'active') return list.filter(t => openStatuses.includes(t.status))
  if (key === 'done') return list.filter(t => t.status === 'completed')
  if (key === 'cancelled') return list.filter(t => t.status === 'cancelled')
  return list
}

function pageTasks (list, page, perPage) {
  const pages = Math.max(1, Math.ceil(list.length / perPage))
  const at = Math.min(Math.max(1, page), pages)
  return { page: at, pages, items: list.slice((at - 1) * perPage, at * perPage) }
}

function taskCard (t) {
  const a = t.attempts[t.attempts.length - 1]
  const quiet = stalledAttempt(t)
  return h('button', {
    type: 'button',
    class: 'tcard s-' + t.status + (st.taskOpen === t.taskId ? ' sel' : ''),
    onclick: async () => {
      st.taskOpen = st.taskOpen === t.taskId ? null : t.taskId
      await renderTasks()
      // The panel opens under the board, which can be a screen away.
      const panel = st.taskOpen && document.querySelector('.tdetail')
      if (panel) panel.scrollIntoView({ block: 'start', behavior: 'smooth' })
    }
  },
  h('div', { class: 'tcardTop' }, h('span', { class: 'tid' }, t.taskId), taskPill(t.status)),
  h('div', { class: 'ttitle' }, t.title),
  h('div', { class: 'tmeta' }, t.projectId + (t.provider ? ' · ' + t.provider : '') + (a ? ' · ' + tr('ครั้งที่ ') + t.attempts.length + '/' + t.maxAttempts : '') + ' · ' + ago(t.updatedAt)),
  a && a.status === 'unknown' ? h('div', { class: 'tcall' }, tr('ไม่ทราบว่า agent ได้รับหรือยัง — คลิกเพื่อยืนยัน')) : null,
  t.status === 'ready' ? h('div', { class: 'tcall' }, tr('รอคุณอนุมัติเริ่มงาน — คลิกเพื่อเปิด')) : null,
  quiet ? h('div', { class: 'tcall' }, quietLine(quiet) + tr(' · คลิกเพื่อยกเลิกหรือรอ')) : null,
  t.status === 'awaitingReview' ? h('div', { class: 'tcall' }, tr('รอคุณตรวจ — คลิกเพื่อดูหลักฐาน')) : null,
  t.blockedReason ? h('div', { class: 'tmeta' }, t.blockedReason) : null)
}

// lastEvidence is what the orchestrator recorded when the task last moved
// with evidence — the commit and, for review, the tests and changed files.
function lastEvidence (events) {
  for (let i = events.length - 1; i >= 0; i--) {
    const d = events[i].data || {}
    if (d.evidence) return d.evidence
  }
  return null
}

function eventLine (e) {
  const d = e.data || {}
  let what = e.type
  if (e.type === 'taskCreated') what = tr('สร้างงาน')
  else if (e.type === 'taskTransitioned') what = (taskStatusLabel[d.from] || d.from) + ' → ' + (taskStatusLabel[d.to] || d.to) + (d.reason ? ' · ' + d.reason : '')
  else if (e.type === 'attemptStarting') what = tr('เริ่ม ') + e.attemptId + (d.workspace ? ' · ' + d.workspace : '')
  else if (e.type === 'attemptRunning') what = attemptLabel.running
  else if (e.type === 'attemptUnknown') what = attemptLabel.unknown
  else if (e.type === 'attemptEnded') what = (attemptLabel[d.status] || d.status) + (d.reason ? ' · ' + d.reason : '')
  return h('li', {}, h('span', { class: 'twhen' }, new Date(e.at).toLocaleString()), ' ', h('b', {}, actorLabel[e.actor] || e.actor), ' · ', what)
}

function taskDetail (data) {
  const t = data.task
  const a = t.attempts[t.attempts.length - 1]
  const ev = lastEvidence(data.events)
  const quiet = stalledAttempt(t)
  const actions = []
  const move = (to, label, needReason, cls) => actions.push(h('button', {
    type: 'button',
    class: cls || '',
    onclick: async () => {
      let reason = ''
      if (needReason) {
        reason = (window.prompt(label + tr(' — เหตุผล')) || '').trim()
        if (!reason) return
      }
      await act('/tasks/' + encodeURIComponent(t.taskId) + '/transition', { to, reason }, () => t.taskId + ' → ' + taskStatusLabel[to])
    }
  }, label))
  if (t.status === 'ready') {
    actions.push(h('button', {
      type: 'button',
      class: 'pri',
      onclick: async () => {
        if (!window.confirm(tr('อนุมัติเริ่มงาน ') + t.taskId + tr('? ระบบจะพิมพ์คำสั่งเข้า session ของ agent ที่เลือก'))) return
        await act('/tasks/' + encodeURIComponent(t.taskId) + '/dispatch', {}, r => tr('ส่งงานแล้ว · ') + (r.workspace || '') + ' · ' + (attemptLabel[r.status] || r.status))
      }
    }, tr('อนุมัติเริ่มงาน')))
  }
  if (a && a.status === 'unknown') {
    actions.push(h('button', {
      type: 'button',
      class: 'pri',
      onclick: () => act('/tasks/' + encodeURIComponent(t.taskId) + '/attempts/' + encodeURIComponent(a.attemptId) + '/confirm', {}, () => tr('ยืนยันแล้ว — agent ได้รับงาน'))
    }, tr('ยืนยันว่า agent ได้รับงานแล้ว')))
  }
  if (t.status === 'awaitingReview') {
    actions.push(h('button', { type: 'button', onclick: () => openTask(t) }, tr('ดู diff ที่หน้ารีวิว')))
    move('completed', tr('อนุมัติ — เสร็จแล้ว'), false, 'pri')
    move('queued', tr('ขอแก้'), true)
  }
  if (t.status === 'blocked') move('queued', tr('ปลดบล็อก'), false)
  if (t.status === 'failed') move('queued', tr('ส่งกลับเข้าคิว'), false)
  if (t.status !== 'completed' && t.status !== 'cancelled') move('cancelled', tr('ยกเลิกงาน'), true, 'warn')
  if (t.status === 'cancelled') actions.push(h('button', { type: 'button', class: 'warn', onclick: () => deleteTask(t) }, tr('ลบงาน')))

  const evRows = ev
    ? [
        ev.tests ? h('div', { class: ev.tests.startsWith('passed') ? 'tok' : 'twarn' }, tr('ผลเทสต์: '), ev.tests) : null,
        ev.commit ? h('div', {}, 'commit: ', h('code', {}, ev.commit)) : null,
        ev.changedFiles ? h('div', {}, tr('ไฟล์ที่แก้: '), ev.changedFiles) : null,
        ev.needsHumanReview ? h('div', { class: 'twarn' }, tr('แตะไฟล์ที่ต้องให้คนดู: '), ev.needsHumanReview) : null
      ]
    : [h('div', { class: 'tmeta' }, tr('ยังไม่มีหลักฐาน'))]

  return h('section', { class: 'tdetail' },
    h('div', { class: 'tcardTop' }, h('span', { class: 'tid' }, t.taskId), taskPill(t.status), h('span', { class: 'tmeta' }, tr('รุ่นที่ ') + t.taskRevision)),
    h('h3', {}, t.title),
    // The buttons come before the long part: a task that waits for the user
    // must show what it waits for without scrolling.
    actions.length ? h('div', { class: 'tactions' }, actions) : null,
    quiet ? h('div', { class: 'twarn' }, quietLine(quiet) + ' · ' + tr('งานนี้ไม่ย้ายเอง: ยกเลิกงาน หรือรอ agent กลับมารายงาน')) : null,
    h('div', { class: 'tcols2' },
      h('div', {},
        h('h4', {}, tr('เกณฑ์ผ่าน')),
        h('ul', {}, t.acceptanceCriteria.map(c => h('li', {}, c))),
        t.allowedPaths && t.allowedPaths.length ? h('div', { class: 'tmeta' }, tr('แก้ได้เฉพาะ: ') + t.allowedPaths.join(', ')) : null,
        h('h4', {}, tr('ไทม์ไลน์')),
        h('ul', { class: 'ttimeline' }, data.events.filter(e => e.type !== 'attemptHeartbeat').map(eventLine))),
      h('div', {},
        h('h4', {}, tr('ความพยายาม')),
        t.attempts.length
          ? h('ul', {}, t.attempts.map(x => h('li', {}, x.attemptId + ' · ' + (x.provider || '') + ' ' + (x.workspace || '') + ' · ' + (attemptLabel[x.status] || x.status) + (x.exitReason ? ' · ' + x.exitReason : ''))))
          : h('div', { class: 'tmeta' }, '—'),
        h('h4', {}, tr('หลักฐาน')),
        evRows,
        t.status === 'awaitingReview' ? h('div', { class: 'tmeta' }, tr('การอนุมัติไม่ใช่การ deploy')) : null)))
}

function taskForm () {
  const sel = (name, opts) => h('select', { name }, opts.map(([v, l]) => h('option', { value: v }, l)))
  const f = h('form', { class: 'tform' },
    h('label', {}, tr('ชื่องาน'), h('input', { name: 'title', required: 'required', maxlength: '300' })),
    h('label', {}, tr('โปรเจกต์'), h('input', { name: 'projectId', required: 'required', maxlength: '100', value: 'aiagent' })),
    h('label', {}, tr('ให้ใครทำ'), sel('provider', [['', tr('อัตโนมัติ (ตาม quota)')], ['claude', 'Claude'], ['codex', 'Codex'], ['gemini', 'Gemini']])),
    h('label', {}, tr('ชนิดงาน'), sel('kind', [['feature', 'feature'], ['bug', 'bug'], ['ux', 'ux'], ['regression', 'regression'], ['chore', 'chore']])),
    h('label', { class: 'wide' }, tr('เกณฑ์ผ่าน (บรรทัดละข้อ)'), h('textarea', { name: 'criteria', required: 'required', rows: '3' })),
    h('label', { class: 'wide' }, tr('แก้ได้เฉพาะ path (บรรทัดละข้อ · เว้นว่าง = ทั้ง repo)'), h('textarea', { name: 'paths', rows: '2', placeholder: 'internal/world/**' })),
    h('button', { type: 'submit', class: 'pri' }, tr('สร้างงาน')))
  f.addEventListener('submit', async e => {
    e.preventDefault()
    const v = n => f.elements[n].value
    const lines = s => s.split('\n').map(x => x.trim()).filter(Boolean)
    const ok = await act('/tasks', {
      title: v('title'),
      projectId: v('projectId'),
      provider: v('provider'),
      kind: v('kind'),
      acceptanceCriteria: lines(v('criteria')),
      allowedPaths: lines(v('paths')),
      idempotencyKey: 'web-' + Date.now() + '-' + Math.random().toString(36).slice(2)
    }, r => tr('สร้างงาน ') + (r.taskId || '') + tr(' แล้ว'))
    if (ok) {
      f.reset()
      f.elements.projectId.value = 'aiagent'
      st.taskFormOpen = false
    }
  })
  return f
}

// ---------- PM, review, and tasks in the village ----------
// The task list and the PM thread are read beside the snapshot. Either can be
// off on a machine — the routes answer 404 — and that is null here. Everything
// below reads null as "not available", never as "none": an empty board and a
// board that could not be read are different things to tell the user.
async function loadBoard () {
  const get = async path => {
    try { return await api(path) } catch (e) {
      if (e.message === 'auth') throw e
      return null
    }
  }
  if (isUser()) { st.tasks = null; st.pm = null; return }
  const [t, pm] = await Promise.all([get('/tasks'), get('/pm')])
  // A read that failed keeps what was last known; the connection banner is
  // what says the page is stale. Only a first read that fails means "off".
  if (t) st.tasks = t.tasks || []
  else if (st.tasks === undefined) st.tasks = null
  if (pm) st.pm = pm
  else if (st.pm === undefined) st.pm = null
}

// The service takes one write a second (writeSpacing in http.go) and refuses
// the next. Two quick taps — confirm one proposal, then the one under it — are
// normal here, so writes from these views wait their turn instead.
let writeQueue = Promise.resolve()
let lastWrite = 0
function spaced (fn) {
  const run = async () => {
    const wait = 1100 - (Date.now() - lastWrite)
    if (wait > 0) await new Promise(resolve => setTimeout(resolve, wait))
    try { return await fn() } finally { lastWrite = Date.now() }
  }
  writeQueue = writeQueue.then(run, run)
  return writeQueue
}

const openStatuses = ['awaitingReview', 'verifying', 'running', 'ready', 'queued', 'blocked', 'failed']
// designBrief §10: the room a task sits in, by its state. A finished or
// cancelled task has left the house.
const taskRoom = { queued: 'plan', ready: 'plan', running: 'code', verifying: 'test', awaitingReview: 'approval', blocked: 'ops', failed: 'ops' }
const providerTitle = { claude: 'Claude', codex: 'Codex', gemini: 'Gemini' }
const agentStateLabel = { ready: tr('พร้อม'), notReady: tr('ยังไม่พร้อม'), notConfigured: tr('ยังไม่ได้ตั้งค่า') }
const taskKinds = ['bug', 'feature', 'ux', 'regression', 'security', 'chore']

function openTasks () {
  return (st.tasks || []).filter(t => openStatuses.includes(t.status))
    .sort((a, b) => openStatuses.indexOf(a.status) - openStatuses.indexOf(b.status) || (a.updatedAt < b.updatedAt ? 1 : -1))
}
function countTasks (status) { return (st.tasks || []).filter(t => t.status === status).length }
function openProposals () {
  let n = 0
  for (const m of (st.pm && st.pm.messages) || []) n += (m.proposals || []).filter(p => p.state === 'open').length
  return n
}

function goView (v) {
  // typing mode hides the stage, so a view picked from the header would open
  // behind it; picking a view is also a way out of typing mode
  if (document.body.classList.contains('typing')) setTyping(false)
  try { localStorage.setItem('aiagentView', v) } catch (e) { /* the view is still switched */ }
  setView(v)
}

async function deleteTask (t) {
  if (!await confirmBox(tr('ลบงานนี้?'), t.taskId + ' · ' + t.title + tr(' จะหายจากกระดาน ประวัติยังเก็บไว้ในบันทึก'), '', tr('ลบเลย'))) return
  st.taskOpen = null
  await act('/tasks/' + encodeURIComponent(t.taskId) + '/delete', {}, () => tr('ลบงานแล้ว'))
}

// A task waiting for the user opens on the review page, where the diff is;
// any other opens on the board, where its history is.
function openTask (t) {
  if (t.status === 'awaitingReview') {
    st.rvOpen = t.taskId
    goView('review')
  } else {
    st.taskOpen = t.taskId
    goView('tasks')
  }
}

function renderNavBadges () {
  const wait = countTasks('awaitingReview') + openPages().length
  const rb = $('reviewBadge')
  rb.hidden = !wait
  rb.textContent = wait ? String(wait) : ''
  const thinking = !!(st.pm && st.pm.thinking)
  const open = openProposals()
  const pb = $('pmBadge')
  pb.hidden = !thinking && !open
  pb.textContent = thinking ? '…' : String(open)
}

function taskRow (t) {
  const go = () => openTask(t)
  return h('div', {
    class: 'trow s-' + t.status,
    tabindex: 0,
    role: 'button',
    onclick: go,
    onkeydown: e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); go() } }
  },
  h('div', { class: 'trowTop' }, h('span', { class: 'tid' }, t.taskId), h('span', { class: 'trowTitle' }, t.title)),
  h('div', { class: 'trowBot' }, taskPill(t.status),
    t.status === 'awaitingReview' ? h('span', { class: 'trowGo' }, tr('ตรวจงาน')) : h('span', { class: 'tmeta' }, ago(t.updatedAt))))
}

// The PM block at the top of the overview panel: what is open, and what is
// waiting for the user.
function pmOverview () {
  if (!st.tasks && !st.pm) return []
  const open = openTasks()
  const wait = countTasks('awaitingReview')
  return [
    h('div', { class: 'pmHead side' }, pmAvatar(),
      h('div', null,
        h('h2', null, tr('PM กลาง'), ' ', st.pm ? h('span', { class: 'pmAdapter' }, st.pm.adapter) : h('span', { class: 'pmAdapter off' }, tr('ยังไม่เปิด'))),
        h('p', { class: 'note' }, !st.tasks ? tr('ไม่ทราบจำนวนงาน — ระบบงานยังไม่เปิด') : wait ? tr('ตอนนี้ ') + wait + tr(' งานรอคุณตรวจ') : tr('ไม่มีงานรอคุณตรวจ')))),
    st.tasks ? h('h3', null, tr('งานของ PM (') + open.length + ')') : null,
    st.tasks ? (open.length ? open.slice(0, 5).map(taskRow) : h('p', { class: 'note' }, tr('ยังไม่มีงานที่เปิดอยู่'))) : null,
    h('div', { class: 'pmSideActs' },
      st.pm ? h('button', { type: 'button', class: 'pri', onclick: () => goView('pm') }, tr('คุยกับ PM')) : null,
      st.tasks ? h('button', { type: 'button', onclick: () => goView('tasks') }, tr('กระดานงาน')) : null,
      wait ? h('button', { type: 'button', onclick: () => goView('review') }, tr('ไปหน้ารีวิว')) : null)
  ]
}

// ----- tasks on the map -----
function taskSummary () {
  const wait = countTasks('awaitingReview')
  const run = countTasks('running') + countTasks('verifying')
  return (wait ? wait + tr(' งานรอคุณตรวจ · ') : '') + (run ? run + tr(' งานกำลังทำ · ') : '')
}

function entranceTaskPills () {
  const wait = countTasks('awaitingReview')
  const run = countTasks('running') + countTasks('verifying')
  return [
    wait ? h('em', { class: 'pill review' }, '◆ ' + wait + tr(' งานรอคุณตรวจ')) : '',
    run ? h('em', { class: 'pill work' }, run + tr(' งานกำลังทำ')) : ''
  ]
}

// Lights and symbols on the house rooms: a room is lit when it has a review
// waiting (Testing), a task at its step, or an agent working in it.
function fillRoomSignals () {
  const open = openTasks()
  const agents = actorsFor()
  const reviews = (st.reviews || []).filter(r => r.status === 'open').length
  for (const key in rooms2) {
    const r = rooms2[key]
    if (!r.sigEl || !r.sigEl.isConnected) continue
    const parts = []
    if (key === 'test' && reviews) parts.push(['🧪', reviews, tr('งานรอตรวจ')])
    const tasks = open.filter(t => taskRoom[t.status] === key).length
    if (tasks) parts.push(['📋', tasks, tr('งานที่ค้างในห้องนี้')])
    const busy = agents.filter(a => a.room === key && (a.mood === 'work' || a.mood === 'ask')).length
    if (busy) parts.push(['🤖', busy, tr('agent ทำงานอยู่ในห้องนี้')])
    const sig = parts.map(p => p.join(':')).join('|')
    r.cardEl.classList.toggle('lit', parts.length > 0)
    r.cardEl.classList.toggle('pending', !!reviews && key === 'test')
    if (r.sigEl.dataset.sig === sig) continue
    r.sigEl.dataset.sig = sig
    r.sigEl.hidden = !parts.length
    r.sigEl.title = parts.map(p => p[0] + ' ' + p[1] + ' ' + p[2]).join('\n')
    r.sigEl.replaceChildren(...parts.map(p => h('span', { class: 'sigChip' }, p[0] + ' ' + p[1])))
  }
}

// Chips on the rooms of the house: which task is at which step.
function fillRoomTasks () {
  const open = openTasks()
  for (const key in rooms2) {
    const el = rooms2[key].taskEl
    if (!el || !el.isConnected) continue
    const mine = open.filter(t => taskRoom[t.status] === key)
    const sig = mine.map(t => t.taskId + t.status).join(',')
    if (el.dataset.sig === sig) continue
    el.dataset.sig = sig
    el.replaceChildren(...mine.slice(0, 2).map(t => h('button', {
      type: 'button',
      class: 'rtChip s-' + t.status,
      title: t.title,
      onclick: e => { e.stopPropagation(); openTask(t) }
    }, h('i', { class: 'dot' }), t.taskId + ' · ' + taskStatusLabel[t.status])),
    ...(mine.length > 2 ? [h('span', { class: 'rtChip more' }, '+' + (mine.length - 2))] : []))
  }
}

// The strip under the village: the tasks that moved last.
function renderTaskStrip () {
  const el = $('taskStrip')
  const show = st.view === 'map' && !st.house && !!st.tasks && st.tasks.length > 0
  const recent = show ? [...st.tasks].sort((a, b) => (a.updatedAt < b.updatedAt ? 1 : -1)).slice(0, 2) : []
  const key = show ? JSON.stringify(recent.map(t => [t.taskId, t.status, t.title, t.updatedAt])) : ''
  if (el.hidden === !show && key === st.stripKey) return
  st.stripKey = key
  const was = el.hidden
  el.hidden = !show
  $('stage').classList.toggle('hasStrip', show)
  if (show) {
    el.replaceChildren(
      h('div', { class: 'tsHead' }, h('b', null, tr('งานในหมู่บ้าน (ล่าสุด)')),
        h('button', { type: 'button', class: 'ghost', onclick: () => goView('tasks') }, tr('ดูทั้งหมด ›'))),
      ...recent.map(t => {
        const room = taskRoom[t.status]
        const go = () => openTask(t)
        return h('div', {
          class: 'tsRow',
          tabindex: 0,
          role: 'button',
          onclick: go,
          onkeydown: e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); go() } }
        },
        h('span', { class: 'tid' }, t.taskId),
        h('span', { class: 'tsTitle' }, t.title),
        taskPill(t.status),
        h('span', { class: 'tsRoom' }, room ? roomShort[room] : '—'),
        h('span', { class: 'tmeta' }, tr('อัปเดต ') + fmtTime(t.updatedAt)),
        h('span', { class: 'tsGo' }, '›'))
      }))
  }
  // the village is fitted to the space the strip leaves it
  if (was !== el.hidden) applyZoom()
}

// ----- the PM view -----
function pmAvatar () {
  return h('img', { class: 'pmAvatar', src: A('cx-cat'), alt: '' })
}

function renderPM () {
  const box = $('pm')
  const pm = st.pm
  if (!pm) {
    st.pmKey = ''
    box.replaceChildren(h('div', { class: 'tasksEmpty' }, tr('PM กลางยังไม่เปิดบนเครื่องนี้ — อัปเดต aiagent แล้ว restart service')))
    return
  }
  const msgs = pm.messages || []
  // Rebuilding the thread on every poll would drop a text selection and an
  // open edit form's focus, so nothing is touched while nothing has changed.
  const key = JSON.stringify([pm, (st.tasks || []).map(t => [t.taskId, t.status, t.title]), st.pmEdit && st.pmEdit.id, pm.thinking && ago(pm.thinkingSince)])
  if (key === st.pmKey && box.childElementCount) return
  st.pmKey = key
  const thread = h('div', { class: 'pmThread' },
    msgs.length
      ? msgs.map(pmMessage)
      : h('div', { class: 'tasksEmpty' },
        h('b', null, tr('เล่าให้ PM ฟังว่าอยากได้อะไร')),
        h('div', null, tr('พิมพ์ที่ช่องด้านล่าง (ปลายทาง: PM กลาง) เช่น “อยากให้ส่งข้อความถึง agent ได้แน่นอน และไม่ส่งซ้ำ” — PM จะแตกเป็นงานพร้อมเกณฑ์ผ่านให้คุณตรวจ แล้วคุณเป็นคนกดยืนยันทีละงาน'))),
    pm.thinking
      ? h('div', { class: 'pmMsg' }, pmAvatar(), h('div', { class: 'pmBody' },
        h('div', { class: 'pmBubble thinking' }, h('i', { class: 'dot' }), tr('PM กำลังคิด… เริ่ม ') + ago(pm.thinkingSince)),
        h('div', { class: 'tmeta' }, tr('ใช้ claude ของเครื่องนี้ (subscription) — ปกติไม่เกินหนึ่งนาที'))))
      : null)
  const head = h('div', { class: 'pmHead' }, pmAvatar(),
    h('div', null,
      h('h2', null, tr('PM กลาง'), ' ', h('span', { class: 'pmAdapter' }, pm.adapter)),
      h('p', { class: 'note' }, tr('วางแผนและติดตามงานกับทีมของคุณ · PM เสนอ คุณเป็นคนยืนยัน'))))
  // On a desktop the thread and the side column each scroll, and both are
  // rebuilt here: carry their positions over, or a poll would jump the page.
  const keep = {}
  for (const q of ['.pmMain', '.pmRail']) {
    const el = box.querySelector(q)
    keep[q] = el ? el.scrollTop : 0
  }
  box.replaceChildren(h('div', { class: 'pmCols' }, h('div', { class: 'pmMain' }, head, thread), pmRail(pm)))
  for (const q in keep) box.querySelector(q).scrollTop = keep[q]
  const n = msgs.length + (pm.thinking ? 1 : 0)
  if (st.pmSeen !== n || st.pmStick) {
    st.pmSeen = n
    st.pmStick = false
    // The last message, not the pane's scrollTop: on a tablet the pane sits in
    // the page's flow and it is the page that scrolls — and there the composer
    // is the row under the thread, so go all the way down to it.
    const last = thread.lastElementChild
    const toEnd = () => {
      if (!last || !last.isConnected) return
      last.scrollIntoView({ block: 'end' })
      const page = document.scrollingElement
      if (page && page.scrollHeight > page.clientHeight) page.scrollTop = page.scrollHeight
    }
    toEnd()
    // The text is measured again once the web font arrives, and grows.
    if (document.fonts && document.fonts.status !== 'loaded') document.fonts.ready.then(toEnd)
  }
}

function pmMessage (m) {
  if (m.role === 'user') {
    return h('div', { class: 'pmMsg me' }, h('div', { class: 'pmBody' }, h('div', { class: 'pmBubble' }, m.text), h('time', null, fmtTime(m.at))))
  }
  const call = m.call
  const props = m.proposals || []
  return h('div', { class: 'pmMsg' }, pmAvatar(), h('div', { class: 'pmBody' },
    m.failed
      ? h('div', { class: 'pmBubble bad' },
        h('b', null, tr('PM ตอบไม่สมบูรณ์')),
        h('div', null, m.text || tr('ไม่ทราบสาเหตุ')),
        h('div', { class: 'tmeta' }, tr('ไม่มีงานถูกสร้าง — ส่งคำขอใหม่ได้ หรือสร้างงานเองที่แท็บ งาน')))
      : m.text ? h('div', { class: 'pmBubble' }, m.text) : null,
    props.map((p, i) => proposalCard(p, i + 1, props)),
    m.questions && m.questions.length
      ? h('div', { class: 'pmAsk' }, h('b', null, tr('PM ถามกลับ')), h('ul', null, m.questions.map(q => h('li', null, q))))
      : null,
    h('div', { class: 'pmFoot' },
      call && call.skill ? h('span', { class: 'pmSkill' }, tr('ทักษะ: ') + call.skill + ' ' + call.version + (call.tries > 1 ? tr(' · ลอง ') + call.tries + tr(' รอบ') : '') + (call.millis ? ' · ' + Math.round(call.millis / 1000) + tr(' วินาที') : '')) : null,
      h('time', null, fmtTime(m.at)))))
}

function proposalCard (p, n, siblings) {
  if (st.pmEdit && st.pmEdit.id === p.id && p.state === 'open') return st.pmEdit.form
  const line = (label, value) => h('div', { class: 'ppLine' }, h('span', null, label), h('div', null, value))
  const who = p.provider ? (providerTitle[p.provider] || p.provider) : tr('อัตโนมัติ (ตาม quota)')
  const deps = (p.dependsOn || []).map(i => tr('ขั้นที่ ') + i + (siblings[i - 1] ? ' (' + siblings[i - 1].title + ')' : ''))
    .concat(p.blockedBy || [])
  let acts
  if (p.state === 'open') {
    acts = [
      h('button', { type: 'button', class: 'pri', onclick: () => confirmProposal(p, null) }, tr('ยืนยันสร้างงาน')),
      h('button', { type: 'button', onclick: () => { st.pmEdit = { id: p.id, form: proposalForm(p, n) }; renderPM() } }, tr('แก้ไข')),
      h('button', { type: 'button', class: 'ghost', onclick: () => dismissProposal(p) }, tr('ไม่เอา'))
    ]
  } else if (p.state === 'confirmed') {
    acts = [
      h('span', { class: 'tok' }, tr('สร้างเป็นงาน ') + p.taskId + tr(' แล้ว')),
      h('button', { type: 'button', onclick: () => { st.taskOpen = p.taskId; goView('tasks') } }, tr('เปิดงาน'))
    ]
  } else {
    acts = [h('span', { class: 'tmeta' }, tr('ไม่เอาข้อเสนอนี้'))]
  }
  return h('section', { class: 'ppCard k-' + p.kind + ' p-' + p.state },
    h('div', { class: 'ppTop' }, h('b', null, n + '. ' + p.title), h('span', { class: 'ppKind' }, p.kind)),
    h('div', { class: 'ppGrid' },
      h('div', { class: 'ppLines' },
        line(tr('โครงการ'), p.projectId),
        line(tr('ผู้ทำ'), who + (p.providerReason ? ' — ' + p.providerReason : '')),
        line(tr('เกณฑ์ผ่าน'), h('ul', null, (p.acceptanceCriteria || []).map(c => h('li', null, c)))),
        p.allowedPaths && p.allowedPaths.length ? line(tr('แก้ได้เฉพาะ'), p.allowedPaths.join(', ')) : null,
        deps.length ? line(tr('ขึ้นกับ'), deps.join(', ')) : null,
        p.riskLabels && p.riskLabels.length ? line(tr('ต้องดูใกล้ชิด'), h('span', { class: 'twarn' }, p.riskLabels.join(', '))) : null),
      h('div', { class: 'ppActs' }, acts)))
}

async function confirmProposal (p, edits) {
  const ok = await spaced(() => act('/pm/proposals/' + encodeURIComponent(p.id) + '/confirm', edits || {},
    r => tr('สร้างงาน ') + ((r.task && r.task.taskId) || '') + tr(' แล้ว — ยังไม่ส่งให้ agent จนกว่าคุณจะอนุมัติเริ่มงานที่แท็บ งาน')))
  if (ok && st.pmEdit && st.pmEdit.id === p.id) {
    st.pmEdit = null
    if (st.view === 'pm') renderPM()
  }
  return ok
}

async function dismissProposal (p) {
  await spaced(() => act('/pm/proposals/' + encodeURIComponent(p.id) + '/dismiss', {}, () => tr('ไม่เอาข้อเสนอนี้แล้ว')))
}

// The edit form is built once and kept in st.pmEdit, so a poll that redraws
// the thread puts the same node back and nothing typed is lost.
function proposalForm (p, n) {
  const pick = (name, opts, cur) => {
    const sel = h('select', { name }, opts.map(([v, l]) => h('option', { value: v }, l)))
    sel.value = cur || opts[0][0]
    return sel
  }
  const area = (name, lines, rows) => {
    const ta = h('textarea', { name, rows: String(rows) })
    ta.value = (lines || []).join('\n')
    return ta
  }
  const f = h('form', { class: 'ppCard ppForm k-' + p.kind },
    h('div', { class: 'ppTop' }, h('b', null, n + '. ' + tr('แก้ข้อเสนอก่อนสร้างงาน'))),
    h('label', { class: 'wide' }, tr('ชื่องาน'), h('input', { name: 'title', required: 'required', maxlength: '300', value: p.title })),
    h('label', null, tr('โปรเจกต์'), h('input', { name: 'projectId', required: 'required', maxlength: '100', value: p.projectId })),
    h('label', null, tr('ให้ใครทำ'), pick('provider', [['', tr('อัตโนมัติ (ตาม quota)')], ['claude', 'Claude'], ['codex', 'Codex'], ['gemini', 'Gemini']], p.provider)),
    h('label', null, tr('ชนิดงาน'), pick('kind', taskKinds.map(k => [k, k]), p.kind)),
    h('label', { class: 'wide' }, tr('เกณฑ์ผ่าน (บรรทัดละข้อ)'), area('criteria', p.acceptanceCriteria, 4)),
    h('label', { class: 'wide' }, tr('แก้ได้เฉพาะ path (บรรทัดละข้อ · เว้นว่าง = ทั้ง repo)'), area('paths', p.allowedPaths, 2)),
    h('div', { class: 'ppActs wide' },
      h('button', { type: 'submit', class: 'pri' }, tr('ยืนยันสร้างงาน')),
      h('button', { type: 'button', onclick: () => { st.pmEdit = null; renderPM() } }, tr('ยกเลิกการแก้'))))
  f.addEventListener('submit', e => {
    e.preventDefault()
    const v = name => f.elements[name].value
    const lines = s => s.split('\n').map(x => x.trim()).filter(Boolean)
    confirmProposal(p, {
      title: v('title'),
      projectId: v('projectId'),
      provider: v('provider'),
      kind: v('kind'),
      acceptanceCriteria: lines(v('criteria')),
      allowedPaths: lines(v('paths'))
    })
  })
  return f
}

function pmRail (pm) {
  const ctx = pm.context || {}
  const projects = ctx.projects || []
  const agents = ctx.agents || []
  const open = openTasks()
  return h('aside', { class: 'pmRail' },
    h('h3', null, tr('บริบทและแผนงาน')),
    h('div', { class: 'pmCtx' },
      h('div', { class: 'pmCtxRow' }, h('span', null, tr('โครงการที่ PM รู้จัก')), h('b', null, projects.length ? projects.join(', ') : tr('ยังไม่มี'))),
      agents.map(a => h('div', { class: 'pmCtxRow' },
        h('span', null, 'Worker: ' + (providerTitle[a.provider] || a.provider)),
        h('b', { class: 'ag ' + a.state }, h('i', { class: 'dot' }), agentStateLabel[a.state] || tr('ไม่ทราบ'))))),
    h('h3', null, tr('งานปัจจุบัน (') + (st.tasks ? open.length : '?') + ')'),
    !st.tasks
      ? h('p', { class: 'note' }, tr('ไม่ทราบ — ระบบงานยังไม่เปิดบนเครื่องนี้'))
      : open.length ? open.slice(0, 6).map(taskRow) : h('p', { class: 'note' }, tr('ยังไม่มีงานที่เปิดอยู่')),
    h('h3', null, tr('แจ้งเตือน')),
    h('div', { class: 'pmNotice' },
      h('b', null, tr('ต้องยืนยันก่อนส่ง worker')),
      h('span', null, tr('PM แค่เสนอ — งานถูกสร้างเมื่อคุณกด “ยืนยันสร้างงาน” และถูกส่งให้ agent เมื่อคุณกด “อนุมัติเริ่มงาน” ที่แท็บ งาน'))),
    h('p', { class: 'note' }, tr('PM อ่านรูปแนบและไฟล์ในเครื่องไม่ได้ — เห็นเฉพาะข้อความ รายชื่อโครงการ งานที่เปิดอยู่ และสถานะ agent')),
    h('button', { type: 'button', class: 'ghost', disabled: pm.thinking || !(pm.messages || []).length, onclick: resetPM }, tr('เริ่มบทสนทนาใหม่')))
}

async function resetPM () {
  const ok = await confirmBox(tr('เริ่มบทสนทนาใหม่กับ PM'), tr('PM จะลืมบทสนทนานี้ และข้อเสนอที่ยังไม่ยืนยันจะหายจากหน้านี้ · งานที่สร้างไปแล้วยังอยู่ครบ'), '', tr('เริ่มใหม่'))
  if (!ok) return
  st.pmEdit = null
  await spaced(() => act('/pm/reset', {}, () => tr('เริ่มบทสนทนาใหม่แล้ว')))
}

// The composer with "PM กลาง" picked. Nothing is confirmed first: a message to
// the PM creates nothing and reaches no agent.
async function sendToPM (ta) {
  const text = expandPastes(ta.value).trim()
  if (!text) return
  if ((st.attach || []).length) { toast(tr('PM ยังอ่านรูปแนบไม่ได้ — เอารูปออกก่อน หรือส่งรูปให้ agent โดยตรง'), true); return }
  if (st.pm && st.pm.thinking) { toast(tr('PM ยังคิดคำตอบก่อนหน้าอยู่ — รอสักครู่'), true); return }
  const sendId = draftSendId(ta, 'pm\n' + text)
  const ok = await spaced(() => act('/pm/messages', { text, sendId }, () => tr('ส่งถึง PM แล้ว — กำลังคิด')))
  if (!ok) return
  forgetSendId(ta)
  ta.value = ''
  clearPastes()
  clearDraft()
  growText()
  st.pmStick = true
  if (st.view !== 'pm') goView('pm')
}

// ----- the review view -----
// One task at a time: what changed, read from the checkout, and the decision
// that only the user makes.
function renderTaskReview () {
  const box = $('review')
  const pages = openPages()
  if (!st.tasks) {
    st.rvKey = ''
    // users have no task board: their review tab is the pages agents filed
    if (isUser()) { box.replaceChildren(reviewList(true)); return }
    box.replaceChildren(pages.length ? reviewList(false) : '', h('div', { class: 'tasksEmpty' }, tr('ระบบงานยังไม่เปิดบนเครื่องนี้ — อัปเดต aiagent แล้ว restart service')))
    return
  }
  const byNew = (a, b) => (a.updatedAt < b.updatedAt ? 1 : -1)
  const waiting = st.tasks.filter(t => t.status === 'awaitingReview').sort(byNew)
  const done = st.tasks.filter(t => t.status === 'completed').sort(byNew).slice(0, 5)
  if (st.rvOpen && !st.tasks.some(t => t.taskId === st.rvOpen)) st.rvOpen = null
  if (!st.rvOpen && waiting.length) st.rvOpen = waiting[0].taskId
  const cur = st.tasks.find(t => t.taskId === st.rvOpen)
  const want = cur ? cur.taskId + '@' + cur.updatedAt : ''
  const have = st.rvData && cur && st.rvData.task.taskId === cur.taskId ? st.rvData : null
  const running = have && have.advisory && have.advisory.state === 'running'
  // The diff is read from git, so it is fetched when the task changes and
  // while a second opinion is on its way — not on every poll.
  if (cur && !st.rvLoading && (st.rvFor !== want || running)) loadTaskReview(want, cur.taskId)
  const key = JSON.stringify([waiting.map(t => [t.taskId, t.title]), done.map(t => t.taskId), pages.map(r => [r.id, r.title]), st.rvOpen, st.rvErr, have, running && ago(have.advisory.at)])
  if (key === st.rvKey && box.childElementCount) return
  st.rvKey = key
  const card = t => h('button', {
    type: 'button',
    class: 'tcard s-' + t.status + (st.rvOpen === t.taskId ? ' sel' : ''),
    onclick: () => { st.rvOpen = t.taskId; renderTaskReview() }
  },
  h('div', { class: 'tcardTop' }, h('span', { class: 'tid' }, t.taskId), taskPill(t.status)),
  h('div', { class: 'ttitle' }, t.title),
  h('div', { class: 'tmeta' }, t.projectId + (t.provider ? ' · ' + t.provider : '') + ' · ' + ago(t.updatedAt)))
  const list = h('div', { class: 'rvList' },
    h('h2', null, tr('รอคุณตรวจ (') + (waiting.length + pages.length) + ')'),
    waiting.length ? waiting.map(card) : pages.length ? null : h('div', { class: 'tasksEmpty' }, tr('ไม่มีงานรอตรวจ — งานจะมาที่นี่เมื่อ agent รายงานว่าเสร็จและระบบตรวจขั้นแรกผ่าน')),
    pages.length ? reviewList(false) : null,
    done.length ? h('h3', null, tr('อนุมัติแล้วล่าสุด')) : null,
    done.map(card))
  let detail
  if (!cur) detail = h('div', { class: 'tasksEmpty' }, tr('เลือกงานทางซ้ายเพื่อดูสิ่งที่เปลี่ยน'))
  else if (have) detail = reviewDetail(have)
  else if (st.rvErr && st.rvFor === want) detail = h('div', { class: 'rvCard unknown' }, h('b', null, tr('ไม่ทราบ — อ่านหน้ารีวิวของงานนี้ไม่ได้')), h('div', null, st.rvErr))
  else detail = h('div', { class: 'tasksEmpty' }, tr('กำลังอ่าน diff จาก checkout…'))
  box.replaceChildren(h('div', { class: 'rvCols' }, list, h('div', { class: 'rvMain' }, detail)))
}

async function loadTaskReview (want, id) {
  st.rvLoading = true
  try {
    st.rvData = await api('/tasks/' + encodeURIComponent(id) + '/review')
    st.rvErr = ''
  } catch (e) {
    st.rvLoading = false
    if (e.message === 'auth') return
    st.rvData = null
    st.rvErr = e.message
  }
  st.rvFor = want
  st.rvLoading = false
  if (st.view === 'review') renderTaskReview()
}

const baseLabel = {
  current: ['ok', tr('ฐานตรงกับ mainline ล่าสุด')],
  behind: ['warn', tr('ฐานเก่ากว่า mainline — มี commit ใหม่กว่าที่งานนี้ยังไม่มี')],
  diverged: ['warn', tr('ฐานแยกจาก mainline')],
  unknown: ['unknown', tr('ไม่ทราบ')]
}
const testsLabel = {
  passed: ['ok', tr('เทสต์ผ่าน')],
  notRun: ['warn', tr('ไม่ได้รันเทสต์')],
  unknown: ['unknown', tr('ไม่ทราบ')]
}
const severityLabel = { info: tr('ข้อสังเกต'), warn: tr('ควรดู'), risk: tr('เสี่ยง') }
const shortSha = s => (s ? s.slice(0, 10) : '—')

function reviewDetail (d) {
  const t = d.task
  const a = d.attempt
  const fact = (title, state, label, detail) => h('div', { class: 'rvCard ' + state }, h('small', null, title), h('b', null, label), detail ? h('div', { class: 'tmeta' }, detail) : null)
  const [testCls, testText] = testsLabel[d.tests.state] || testsLabel.unknown
  const [baseCls, baseText] = baseLabel[d.base.state] || baseLabel.unknown
  const guarded = (d.files || []).filter(f => f.protected)
  const baseDetail = [d.base.commit ? tr('ฐาน ') + shortSha(d.base.commit) : '', d.base.mainlineRef ? d.base.mainlineRef + ' ' + shortSha(d.base.mainlineCommit) : ''].filter(Boolean).join(' · ')
  return h('section', { class: 'rvDetail' },
    h('div', { class: 'tcardTop' }, h('span', { class: 'tid' }, t.taskId), taskPill(t.status), t.kind ? h('span', { class: 'ppKind' }, t.kind) : null),
    h('h3', null, t.title),
    h('div', { class: 'tmeta' }, t.projectId +
      (a ? ' · ' + (providerTitle[a.provider] || a.provider || '—') + ' · ' + (a.workspace || '—') + ' · ' + a.attemptId + ' · commit ' + shortSha(a.commit) : tr(' · ยังไม่มีความพยายามที่รายงาน commit'))),
    reviewDecision(d, guarded),
    h('div', { class: 'rvFacts' },
      fact(tr('เทสต์'), testCls, testText, d.tests.detail),
      fact(tr('ฐานของงาน'), baseCls, baseText, baseDetail),
      fact(tr('สแกนความปลอดภัย'), 'unknown', tr('ไม่ได้สแกน'), tr('ยังไม่มีตัวสแกนในระบบนี้ — ไม่ได้แปลว่าปลอดภัย')),
      d.diffError
        ? fact(tr('ไฟล์ที่ต้องให้คนดู'), 'unknown', tr('ไม่ทราบ'), tr('อ่าน diff ไม่ได้'))
        : guarded.length
          ? fact(tr('ไฟล์ที่ต้องให้คนดู'), 'warn', guarded.length + tr(' ไฟล์'), guarded.map(f => f.path).join(', '))
          : fact(tr('ไฟล์ที่ต้องให้คนดู'), 'ok', tr('ไม่มี'), '')),
    t.riskLabels && t.riskLabels.length ? h('div', { class: 'twarn' }, tr('งานนี้ติดป้ายความเสี่ยง: ') + t.riskLabels.join(', ')) : null,
    h('h4', null, tr('เกณฑ์ผ่าน')),
    h('ul', { class: 'rvCriteria' }, (t.acceptanceCriteria || []).map(c => h('li', null, c))),
    t.allowedPaths && t.allowedPaths.length ? h('div', { class: 'tmeta' }, tr('แก้ได้เฉพาะ: ') + t.allowedPaths.join(', ')) : null,
    advisoryCard(d),
    reviewFiles(d),
    reviewDiff(d))
}

// The reason box is one node kept across redraws, like the PM edit form.
function reviewReason (id) {
  if (!st.rvReason || st.rvReason.id !== id) {
    st.rvReason = { id, el: h('textarea', { rows: '2', maxlength: '2000', placeholder: tr('เหตุผล — ต้องมีเมื่อขอแก้ · agent จะได้รู้ว่าต้องแก้อะไร') }) }
  }
  return st.rvReason.el
}

function reviewDecision (d, guarded) {
  const t = d.task
  if (t.status !== 'awaitingReview') {
    return h('div', { class: 'rvDecide past' }, tr('งานนี้อยู่ในสถานะ “') + (taskStatusLabel[t.status] || t.status) + tr('” — ดูย้อนหลังได้ ไม่มีอะไรให้ตัดสิน'))
  }
  const reason = reviewReason(t.taskId)
  const decide = async to => {
    const why = reason.value.trim()
    if (to === 'queued' && !why) {
      toast(tr('ขอแก้ต้องบอกเหตุผล — agent จะได้รู้ว่าต้องแก้อะไร'), true)
      reason.focus()
      return
    }
    if (to === 'completed') {
      const flags = [
        d.diffError ? tr('• ไม่ทราบว่าเปลี่ยนอะไร — อ่าน diff ไม่ได้') : '',
        d.tests.state !== 'passed' ? tr('• ไม่มีหลักฐานว่าเทสต์ผ่าน') : '',
        d.base.state !== 'current' ? tr('• ฐานของงานไม่ตรงกับ mainline ล่าสุด หรือไม่ทราบ') : '',
        guarded.length ? tr('• แตะไฟล์ที่ต้องให้คนดู: ') + guarded.map(f => f.path).join(', ') : '',
        tr('• ไม่ได้สแกนความปลอดภัย')
      ].filter(Boolean).join('\n')
      const ok = await confirmBox(tr('อนุมัติว่างานนี้เสร็จแล้ว'), t.taskId + ' · ' + t.title + tr(' — การอนุมัติไม่ใช่การ deploy และไม่ merge ให้'), flags, tr('อนุมัติ'))
      if (!ok) return
    }
    const sent = await spaced(() => act('/tasks/' + encodeURIComponent(t.taskId) + '/transition', { to, reason: why }, () => t.taskId + ' → ' + taskStatusLabel[to]))
    if (sent) {
      st.rvReason = null
      st.rvOpen = null
    }
  }
  return h('div', { class: 'rvDecide' },
    h('h4', null, tr('การตัดสินใจของคุณ')),
    reason,
    h('div', { class: 'ppActs' },
      h('button', { type: 'button', class: 'pri', onclick: () => decide('completed') }, tr('อนุมัติ — เสร็จแล้ว')),
      h('button', { type: 'button', class: 'warn', onclick: () => decide('queued') }, tr('ขอแก้ — ส่งกลับเข้าคิว')),
      h('span', { class: 'tmeta' }, tr('มีแต่คุณที่อนุมัติได้ · การอนุมัติไม่ใช่การ deploy'))))
}

function advisoryCard (d) {
  const adv = d.advisory
  const t = d.task
  const ask = label => h('button', {
    type: 'button',
    disabled: !!d.diffError,
    title: d.diffError ? tr('ไม่มี diff ให้อ่าน') : '',
    onclick: async () => {
      const ok = await spaced(() => act('/tasks/' + encodeURIComponent(t.taskId) + '/advisory', {}, () => tr('ขอความเห็นที่สองแล้ว — กำลังอ่าน diff')))
      if (ok) st.rvFor = ''
    }
  }, label)
  let body
  if (!d.advisoryAvailable) {
    body = [h('div', { class: 'tmeta' }, tr('ยังไม่เปิด — ต้องเปิด PM กลางบนเครื่องนี้ก่อน'))]
  } else if (!adv) {
    body = [h('div', { class: 'tmeta' }, tr('ยังไม่ได้ขอ — ให้ AI อีกตัวอ่าน diff แล้วบอกว่าควรดูตรงไหน')), ask(tr('ขอความเห็นที่สอง'))]
  } else if (adv.state === 'running') {
    body = [h('div', { class: 'pmBubble thinking' }, h('i', { class: 'dot' }), tr('กำลังอ่าน diff… เริ่ม ') + ago(adv.at))]
  } else if (adv.state === 'failed') {
    body = [h('div', { class: 'twarn' }, tr('ขอความเห็นไม่สำเร็จ: ') + (adv.error || tr('ไม่ทราบสาเหตุ'))), ask(tr('ขออีกครั้ง'))]
  } else {
    const adv2 = adv.advice || {}
    const concerns = adv2.concerns || []
    const checks = adv2.checks || []
    body = [
      h('p', null, adv2.summary || ''),
      concerns.length
        ? h('ul', { class: 'rvConcerns' }, concerns.map(c => h('li', { class: 'sev-' + c.severity },
          h('span', { class: 'sev' }, severityLabel[c.severity] || c.severity), c.file ? h('code', null, c.file) : null, ' ', c.note)))
        : h('div', { class: 'tmeta' }, tr('ไม่มีข้อสังเกต — ไม่ได้แปลว่าไม่มีปัญหา')),
      checks.length ? h('div', null, h('b', null, tr('ลองเองก่อนตัดสินใจ')), h('ul', null, checks.map(c => h('li', null, c)))) : null,
      h('div', { class: 'pmFoot' },
        adv.call && adv.call.skill ? h('span', { class: 'pmSkill' }, tr('ทักษะ: ') + adv.call.skill + ' ' + adv.call.version) : null,
        h('time', null, fmtTime(adv.at)),
        ask(tr('ขอใหม่')))
    ]
  }
  return h('div', { class: 'rvAdvisory' },
    h('h4', null, tr('คำแนะนำ (advisory) — ไม่ใช่ผู้ตัดสิน')),
    body,
    h('div', { class: 'tmeta' }, tr('ความเห็นจาก AI ที่อ่าน diff อย่างเดียว ไม่ได้รันโค้ด · คุณเป็นคนตัดสิน')))
}

function reviewFiles (d, root) {
  if (d.diffError) {
    return h('div', { class: 'rvCard unknown wide' }, h('small', null, tr('ไฟล์ที่เปลี่ยน')), h('b', null, tr('ไม่ทราบ — อ่าน diff ไม่ได้')), h('div', { class: 'tmeta' }, d.diffError))
  }
  const files = d.files || []
  const jump = path => {
    for (const el of (root || $('review')).querySelectorAll('.rvDiff .f')) {
      if (el.dataset.file === path) { el.scrollIntoView({ block: 'start' }); return }
    }
  }
  return h('div', null,
    h('h4', null, tr('ไฟล์ที่เปลี่ยน (') + files.length + ')'),
    files.length
      ? h('ul', { class: 'rvFiles' }, files.map(f => h('li', null,
        h('button', { type: 'button', class: 'ghost', onclick: () => jump(f.path) }, h('code', null, f.path)),
        f.binary ? h('span', { class: 'tmeta' }, 'binary') : [h('span', { class: 'add' }, '+' + f.added), h('span', { class: 'del' }, '−' + f.deleted)],
        f.protected ? h('span', { class: 'sev' }, tr('ต้องให้คนดู')) : null)))
      : h('div', { class: 'tmeta' }, tr('commit นี้ไม่ได้เปลี่ยนไฟล์ใด')))
}

function reviewDiff (d) {
  if (d.diffError) return null
  if (!d.diff) return h('div', { class: 'tmeta' }, tr('ไม่มีบรรทัดที่เปลี่ยน'))
  const pre = h('pre', { class: 'rvDiff' })
  for (const line of d.diff.split('\n')) {
    let cls = ''
    if (line.startsWith('diff --git ')) cls = 'f'
    else if (line.startsWith('@@')) cls = 'hunk'
    else if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('index ')) cls = 'meta'
    else if (line.startsWith('+')) cls = 'add'
    else if (line.startsWith('-')) cls = 'del'
    const row = h('span', { class: cls }, line || ' ')
    if (cls === 'f') {
      const at = line.lastIndexOf(' b/')
      if (at > 0) row.dataset.file = line.slice(at + 3)
    }
    pre.append(row)
  }
  return h('div', null,
    h('h4', null, 'diff'),
    d.diffTruncated ? h('div', { class: 'twarn' }, tr('diff ยาวเกินที่หน้านี้แสดง — เห็นเฉพาะส่วนต้น ดูทั้งหมดด้วย git diff ใน workspace')) : null,
    pre)
}

// Run last: these use the const bindings declared above them, which are in the
// temporal dead zone until this point in the script. Calling them earlier
// threw a ReferenceError before the page had drawn anything.
enhanceDialogs()
initPanelResize()
initLang()
