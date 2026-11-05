'use strict'
// AI Agent World — read live snapshots, draw the village, send work via
// the token-gated API. Every value from the server goes through textContent,
// nothing is ever injected as HTML.

const tokenKey = 'aiagentToken'
const pollMs = 4000
const staleMs = 12000
const ns = 'http://www.w3.org/2000/svg'

const st = {
  token: safeGet(tokenKey),
  snap: null,
  lastOk: 0,
  view: 'map',
  sel: null,
  detail: null,
  runLog: null,
  timer: null
}

function safeGet (k) {
  try { return sessionStorage.getItem(k) || '' } catch (e) { return '' }
}
function safeSet (k, v) {
  try {
    if (v) sessionStorage.setItem(k, v)
    else sessionStorage.removeItem(k)
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
  codeEdit: 'กำลังเขียนโค้ด',
  testRun: 'กำลังทดสอบ',
  deploy: 'กำลัง Deploy',
  research: 'กำลังอ่าน/วิเคราะห์',
  command: 'กำลังรันคำสั่ง',
  planning: 'กำลังวางแผน',
  idle: 'ว่าง รอคำสั่ง',
  unknown: 'กำลังทำงาน'
}
const statusLabel = { running: 'ทำงานอยู่', idle: 'ว่าง รอคำสั่ง', ended: 'จบ session แล้ว', needsInput: 'รอคุณตอบ', completed: 'เสร็จแล้ว' }
function stateText (sess) {
  if (!sess.live) return statusLabel.ended
  if (sess.status === 'running') return activityLabel[sess.activityType] || statusLabel.running
  return statusLabel[sess.status] || sess.status
}
const runLabel = {
  RUNNING: 'กำลังทำ',
  WAITING_DECISION: 'รอคุณตัดสินใจ',
  READY: 'เสร็จ รอตรวจ',
  DONE: 'เสร็จแล้ว',
  PAUSED: 'พัก (บันทึกไว้)',
  ABORTED: 'ยกเลิกแล้ว'
}
const modeLabel = { normal: 'ปกติ', eco: 'ประหยัด', critical: 'วิกฤต', reserve: 'สำรองไว้', guarded: 'ไม่ทราบ' }
const providerName = { openai: 'Codex', anthropic: 'Claude' }
const runtimeName = { claudeCode: 'Claude Code', codexCli: 'Codex CLI' }
const evidenceLabel = {
  'pid+procStart': 'ยืนยันจาก process (pid + เวลาเริ่ม)',
  openFileHandle: 'ยืนยันจาก process ที่เปิดไฟล์ session',
  processCwd: 'มี process codex ในโฟลเดอร์เดียวกัน',
  none: 'ไม่มี process ทำงานอยู่'
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
  if (sec < 60) return sec + ' วินาทีที่แล้ว'
  if (sec < 3600) return Math.round(sec / 60) + ' นาทีที่แล้ว'
  if (sec < 86400) return Math.round(sec / 3600) + ' ชั่วโมงที่แล้ว'
  return Math.round(sec / 86400) + ' วันที่แล้ว'
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
async function api (path, opts) {
  const res = await fetch('/aiagent/api' + path, {
    method: (opts && opts.method) || 'GET',
    headers: Object.assign({ 'X-Aiagent-Token': st.token }, opts && opts.body ? { 'Content-Type': 'application/json' } : {}),
    body: opts && opts.body ? JSON.stringify(opts.body) : undefined,
    cache: 'no-store'
  })
  let body = null
  try { body = await res.json() } catch (e) { body = null }
  if (res.status === 401 || res.status === 429) {
    showLogin(body && body.message)
    throw new Error('auth')
  }
  if (!res.ok) throw new Error((body && body.message) || ('HTTP ' + res.status))
  return body ? body.details : null
}

// ---------- polling ----------
async function poll () {
  clearTimeout(st.timer)
  if (!st.token) { showLogin(); return }
  try {
    st.snap = await api('/snapshot')
    st.lastOk = Date.now()
    if (!st.revAt || Date.now() - st.revAt > 10000) {
      st.revAt = Date.now()
      try { st.reviews = (await api('/reviews')).reviews || [] } catch (e) { /* keep */ }
    }
    render()
  } catch (e) {
    if (e.message === 'auth') return
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
    c.className = 'conn ok'
    c.textContent = 'สด · ' + fmtTime(st.snap && st.snap.generatedAt)
  } else {
    c.className = 'conn bad'
    c.textContent = st.lastOk ? 'ขาดการเชื่อมต่อ · ข้อมูลล่าสุด ' + fmtTime(new Date(st.lastOk).toISOString()) : 'ยังเชื่อมต่อไม่ได้'
  }
  const b = $('banner')
  if (!fresh && st.lastOk) {
    b.hidden = false
    b.className = 'banner bad'
    b.textContent = 'ขาดการเชื่อมต่อกับบริการ — ภาพด้านล่างเป็นข้อมูลเก่า ไม่ใช่สถานะปัจจุบัน'
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
    b.textContent = 'AI กำลังประหยัดพลังงาน — ' + providerName[worst.providerId] + ' ใช้ไป ' + Math.round(worst.usedPercent) + '% (' + modeLabel[worst.mode] + ')' + (worst.resetsAt ? ' · รีเซ็ต ' + new Date(worst.resetsAt).toLocaleString('th-TH', { dateStyle: 'short', timeStyle: 'short' }) : '')
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
  $('host').textContent = snap.host || ''
  const live = snap.sessions.filter(x => x.live)
  const reach = live.filter(x => x.dispatchVia).length
  const hs = st.house && houseByKey[st.house]
  const rf = hs && st.roomFocus && roomTitle[st.roomFocus]
  $('stageTitle').textContent = rf ? hs.name + ' · ' + rf : hs ? hs.name : 'หมู่บ้าน Agent'
  applyPhase()
  $('stageBack').hidden = !hs
  $('stageSub').textContent = rf
    ? 'จอมอนิเตอร์ห้อง · ข้อความล่าสุดของ agent ในห้องนี้ · คลิก agent เพื่อเปิดแชต · Esc/← กลับ'
    : hs
    ? hs.sub + ' · คลิกห้องเพื่อซูม · คลิก agent เพื่อดูบทสนทนา · อัปเดต ' + ago(snap.generatedAt)
    : live.length + ' agent · ' + reach + ' รับข้อความจากเว็บได้ (● เขียว) · ' + snap.summary.needsInput + ' รอคุณตอบ · คลิกบ้านเพื่อเข้าไปดู'
  applyWeather()
  checkLaunches()
  renderEnergy()
  renderStats()
  renderComposerTargets()
  if (st.view === 'map') renderMap()
  else renderList()
  renderInspector()
  followTick()
}

function renderEnergy () {
  const box = $('energy')
  box.replaceChildren()
  const order = ['normal', 'eco', 'critical', 'reserve']
  const byProv = {}
  for (const q of st.snap.quotas) (byProv[q.providerId] = byProv[q.providerId] || []).push(q)
  for (const pid of ['anthropic', 'openai'].filter(x => byProv[x]).concat(Object.keys(byProv).filter(x => x !== 'anthropic' && x !== 'openai'))) {
    const qs = byProv[pid]
    const label = providerName[pid] || pid
    const parts = qs.map(q => q.confidence === 'actual'
      ? (q.windowMinutes ? windowName(q.windowMinutes) + ' ' : '') + Math.round(q.usedPercent) + '%'
      : q.confidence === 'stale' ? (q.windowMinutes ? windowName(q.windowMinutes) + ' ' : '') + 'รีเซ็ตแล้ว' : null).filter(Boolean)
    const worst = qs.reduce((a, q) => order.indexOf(q.mode) > order.indexOf(a) ? q.mode : a, qs.some(q => q.confidence === 'actual') ? 'normal' : 'guarded')
    const text = parts.length ? label + ' ใช้ไป ' + parts.join(' · ') : label + ' quota ไม่ทราบ'
    box.append(h('span', { class: 'pill ' + worst + ' prov-' + pid, title: qs.map(q => q.source + (q.resetsAt ? ' · รีเซ็ต ' + new Date(q.resetsAt).toLocaleString('th-TH', { dateStyle: 'short', timeStyle: 'short' }) : '')).join('\n') }, h('i'), text))
  }
}
function windowName (m) {
  if (m >= 10080) return 'รายสัปดาห์'
  if (m >= 1440) return Math.round(m / 1440) + ' วัน'
  return Math.round(m / 60) + ' ชม.'
}

function renderStats () {
  const sm = st.snap.summary
  const items = [
    ['running', sm.running, 'กำลังทำงาน', ''],
    ['needsInput', sm.needsInput, 'รอคุณตอบ', sm.needsInput ? 'warn' : ''],
    ['rest', sm.idle + sm.completed, 'ว่าง / เสร็จแล้ว', ''],
    ['runsDone', sm.runsDoneToday, 'งานเสร็จวันนี้ (kcmd)', ''],
    ['waitingApproval', sm.waitingApproval, 'รอตัดสินใจ', sm.waitingApproval ? 'warn' : ''],
    ['lost', sm.lost, 'ขาดการเชื่อมต่อ', sm.lost ? 'bad' : '']
  ]
  $('stats').replaceChildren(...items.map(([key, n, l, c]) => h('button', { type: 'button', class: 'stat ' + c + (st.filter === key && !st.sel ? ' on' : ''), onclick: () => openFilter(key), 'aria-label': l + ' ' + n + ' — ดูรายการ' }, h('b', null, n), h('span', null, l))))
}

function openFilter (key) {
  st.filter = key
  st.sel = null
  st.inspKey = ''
  render()
}

const filterTitle = { running: 'กำลังทำงาน', needsInput: 'รอคุณตอบ', rest: 'ว่าง / เสร็จแล้ว', runsDone: 'งาน kcmd เสร็จวันนี้', waitingApproval: 'รอตัดสินใจ (kcmd)', lost: 'ขาดการเชื่อมต่อ' }

function houseName (sess) { return (houseByKey[originOf(sess)] || {}).name || '' }

function filterPanel (key) {
  const snap = st.snap
  const since = new Date()
  since.setHours(0, 0, 0, 0)
  let cards = []
  if (key === 'running' || key === 'needsInput' || key === 'rest') {
    const want = key === 'rest' ? ['idle', 'completed'] : [key]
    cards = snap.sessions.filter(x => x.live && want.includes(x.status)).map(sess => {
      const ex = h('div', { class: 'excerpt', 'data-sid': sess.sessionId }, key === 'needsInput' ? 'กำลังโหลดข้อความล่าสุด…' : (sess.title || ''))
      return h('div', { class: 'card ' + (key === 'needsInput' ? 'warn' : ''), tabindex: 0, onclick: () => { st.tab = 'chat'; flyTo('session', sess.sessionId) }, onkeydown: e => { if (e.key === 'Enter') { st.tab = 'chat'; flyTo('session', sess.sessionId) } } },
        h('b', null, sess.displayName),
        h('small', null, houseName(sess) + ' · ' + stateText(sess) + ' · ' + ago(sess.lastEventAt) + (sess.messageVia ? ' · ตอบจากเว็บได้' : ' · ดูได้อย่างเดียว')),
        ex)
    })
  } else {
    const runs = snap.runs.filter(r => key === 'lost' ? r.lost : key === 'waitingApproval' ? r.status === 'WAITING_DECISION' : (['DONE', 'READY'].includes(r.status) && new Date(r.updatedAt) >= since))
    cards = runs.map(run => h('div', { class: 'card', tabindex: 0, onclick: () => select('run', run.id) }, h('b', null, run.id + ' · ' + run.workspace), h('small', null, (runLabel[run.status] || run.status) + ' · ' + ago(run.updatedAt)), h('div', { class: 'excerpt' }, run.task.slice(0, 160))))
  }
  if (key === 'needsInput') loadExcerpts()
  return [
    h('button', { type: 'button', class: 'ghost', onclick: () => { st.filter = null; render() } }, '← ภาพรวม'),
    h('h2', null, filterTitle[key] + ' (' + cards.length + ')'),
    cards.length ? cards : h('p', { class: 'note' }, 'ไม่มีรายการ'),
    key === 'needsInput' ? h('p', { class: 'note' }, 'คลิกรายการเพื่อเปิดบทสนทนาแล้วพิมพ์ตอบได้ทันที') : null
  ]
}

async function loadExcerpts () {
  for (const sess of st.snap.sessions.filter(x => x.live && x.status === 'needsInput')) {
    try {
      const res = await api('/sessions/' + encodeURIComponent(sess.sessionId) + '/messages?limit=4')
      const last = (res.messages || []).filter(m => m.role === 'assistant').pop()
      const el = document.querySelector('.excerpt[data-sid="' + sess.sessionId + '"]')
      if (el) el.textContent = last ? last.text.slice(0, 220) : 'ไม่มีข้อความ'
    } catch (e) { /* keep placeholder */ }
  }
}

function renderComposerTargets () {
  const sel = $('askWs')
  const cur = st.target !== undefined ? st.target : sel.value
  sel.replaceChildren()
  const ready = st.snap.workspaces.filter(w => w.agentReady)
  sel.append(h('option', { value: '' }, ready.length ? '— เลือกปลายทาง —' : 'ไม่มี agent พร้อมรับงาน'))
  for (const w of st.snap.workspaces) {
    if (!w.dirExists) continue
    const label = w.name + (w.agentReady ? ' · ' + w.agentCommand + ' (tmux ' + w.tmuxSession + ')' : w.tmuxAlive ? ' · ไม่มี agent (shell)' : ' · ปิดอยู่')
    sel.append(h('option', { value: w.name, disabled: !w.agentReady }, label))
  }
  // never pick a target for the operator: only an explicit choice or the
  // selected agent's own workspace
  sel.value = cur && ready.some(w => w.name === cur) ? cur : ''
  updateComposerHint()
}

function composerSession () {
  if (!st.sel || st.sel.kind !== 'session' || !st.snap) return null
  const sess = st.snap.sessions.find(x => x.sessionId === st.sel.id)
  return sess && sess.live && sess.messageVia ? sess : null
}

function updateComposerHint () {
  const sess = composerSession()
  if (!sess) st.suggest = []
  setTimeout(drawSuggest, 0)
  const chip = $('askTarget')
  $('askWs').hidden = !!sess
  $('askMode').hidden = !!sess
  chip.hidden = !sess
  if (sess) {
    chip.replaceChildren(h('span', null, 'ส่งถึง: ' + sess.displayName), h('small', null, sess.messageVia === 'relay' ? 'relay' : 'kcmd ' + sess.dispatchVia), h('button', { type: 'button', class: 'ghost', title: 'เปลี่ยนเป็นส่งตาม workspace', onclick: () => { back() } }, '×'))
    $('askText').placeholder = 'พิมพ์ถึง ' + sess.displayName + '… (Enter ส่ง · Shift+Enter ขึ้นบรรทัด · วาง/ลากรูปได้)'
    $('askGo').disabled = false
    return
  }
  const ws = $('askWs').value
  const w = ws && st.snap && st.snap.workspaces.find(x => x.name === ws)
  const input = $('askText')
  if (st.blockReason) {
    input.placeholder = st.blockReason
  } else if (w) {
    input.placeholder = 'ส่งถึง ' + w.name + ' (' + w.agentCommand + ' ใน tmux ' + w.tmuxSession + ')…'
  } else {
    input.placeholder = 'เลือกปลายทางก่อน หรือคลิกตัวละครที่รับงานได้'
  }
  $('askGo').disabled = !w
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

const houses = [
  { key: 'kcmd', name: 'บ้าน kcmd', sub: 'tmux · สั่งงานจากเว็บได้', img: 'house-kcmd', x: 250, y: 300, w: 330, icon: 'code', tone: 'green' },
  { key: 'daemon', name: 'บ้าน Background', sub: 'Claude daemon · ส่งข้อความผ่าน relay', img: 'house-bg', x: 640, y: 215, w: 380, icon: 'term', tone: 'purple' },
  { key: 'terminal', name: 'บ้าน Terminal · VS Code', sub: 'เปิดในเครื่องของผู้ใช้ · ดูได้อย่างเดียว', img: 'house-term', x: 1080, y: 300, w: 330, icon: 'code', tone: 'orange' }
]
const houseByKey = Object.fromEntries(houses.map(x => [x.key, x]))

function originOf (sess) {
  if (sess.messageVia === 'kcmd') return 'kcmd'
  if (sess.kind === 'bg') return 'daemon'
  return 'terminal'
}

const roomOfActivity = {
  codeEdit: 'code', testRun: 'test', deploy: 'deploy', command: 'ops', research: 'library', planning: 'plan', unknown: 'plan', idle: 'lounge'
}
const earColors = ['#F26D7D', '#6C8CF5', '#9B7BE0', '#F2A65A', '#3FC1C9', '#F08CC0', '#7BC96F', '#E8C547']

function actorsFor (houseKey) {
  const out = []
  for (const sess of st.snap.sessions) {
    if (!sess.live || originOf(sess) !== houseKey) continue
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
      label: waiting ? 'รอคุณตอบ' : (resting && ci) ? 'รอ CI: ' + ci.label : (resting && rev) ? 'รอคุณตรวจงาน (' + rev + ')' : s2 === 'completed' ? 'เสร็จแล้ว' : resting ? 'ว่าง' : activityLabel[sess.activityType],
      watches: sess.watches || [],
      name: (sess.dispatchVia ? '@' + sess.dispatchVia + ' ' : '') + sess.displayName,
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
        label: done ? 'เสร็จแล้ว' : (activityLabel[sa.activityType] || 'กำลังช่วย'),
        name: '↳ ' + (sa.type && sa.type !== 'general-purpose' ? sa.type + ': ' : '') + sa.description,
        canTake: false
      })
    }
  }
  if (houseKey === 'kcmd') {
    for (const run of st.snap.runs) {
      if (run.status === 'WAITING_DECISION') out.push({ kind: 'run', id: run.id, room: 'approval', runtime: 'claudeCode', mood: 'ask', label: 'รอคุณตัดสินใจ', name: run.id })
      else if (run.lost) out.push({ kind: 'run', id: run.id, room: 'ops', runtime: 'claudeCode', mood: 'lost', label: 'ขาดการเชื่อมต่อ', name: run.id })
    }
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

function applyZoom () {
  const v = $('village')
  const b = st.board
  if (!b || !b.el.isConnected) return
  const fit = Math.min(v.clientWidth / b.w, v.clientHeight / b.h) || 1
  const z = fit * (st.zoom || 1)
  b.el.style.transform = 'translate(' + Math.max(0, (v.clientWidth - b.w * z) / 2) + 'px,' + Math.max(0, (v.clientHeight - b.h * z) / 2) + 'px) scale(' + z + ')'
  v.classList.toggle('zoomed', (st.zoom || 1) > 1)
  $('zoomVal').textContent = Math.round((st.zoom || 1) * 100) + '%'
}

function renderMap () {
  if (st.house && st.roomFocus) renderRoomFocus()
  else if (st.house) renderInterior()
  else renderVillage()
}

function renderVillage () {
  const counts = {}
  for (const hs of houses) counts[hs.key] = actorsFor(hs.key)
  const all = houses.flatMap(hs => counts[hs.key].map(a => Object.assign({ house: hs.key }, a)))
  const sig = houses.map(hs => counts[hs.key].map(a => a.mood + a.room).join(',')).join('|')
  if (st.drawn === 'village' && st.villageSig === sig) return
  st.villageSig = sig
  // the scenery is drawn once; a status change only redraws the houses
  if (st.drawn === 'village' && st.vBoard && st.vBoard.isConnected) {
    for (const el of st.vBoard.querySelectorAll('.houseHit')) el.remove()
    drawVillageHouses(st.vBoard, counts, all)
    return
  }
  st.drawn = 'village'
  st.actors = {}
  const b = sceneBoard(VW, VH, 'village')
  st.vBoard = b
  drawVillageScenery(b)
  drawVillageHouses(b, counts, all)
}

function drawVillageScenery (b) {
  let seed = 11
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
  b.append(img('cloud1', 40, 120, 170, { cls: 'drift' }), img('cloud2', 1440, 90, 150, { cls: 'drift slow' }), img('cloud1', 980, 40, 120, { cls: 'drift slow' }), img('cloud2', 330, 60, 110, { cls: 'drift' }))
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
  b.append(isl)
  const deco = [
    ['sakura', 20, 330, 170], ['tree1', 440, 160, 120], ['tree2', 1160, 170, 120], ['sakura', 1490, 310, 160], ['tree1', 1560, 520, 110], ['tree2', -10, 560, 120],
    ['tree2', 600, 170, 90], ['tree1', 960, 160, 95], ['sakura2', 700, 420, 230],
    ['bush2', 420, 470, 80], ['bush3', 1170, 470, 80], ['bush1', 560, 560, 60], ['bush2b', 1060, 560, 80], ['flowerbox3', 300, 800, 120], ['flowerbox3', 1240, 800, 120],
    ['lamppost2', 490, 560, 44], ['lamppost2', 1130, 560, 44], ['lamppost2', 690, 760, 40], ['lamppost2', 930, 760, 40],
    ['bench2', 600, 650, 120], ['board-cork', 990, 610, 110], ['fence', 430, 820, 130], ['fence', 1100, 820, 130], ['fountain', 750, 590, 160], ['gate', 640, 800, 380]
  ]
  for (const [n, x, y, w] of deco) {
    const el = img(n, x, y, w, { z: Math.round(y) })
    if (n.startsWith('lamppost')) el.classList.add('lampGlow')
    b.append(el)
  }
}

// ----- A1 weather follows the plan usage -----
function applyWeather () {
  const used = (st.snap.quotas || []).filter(q => q.confidence === 'actual' && q.usedPercent != null).map(q => q.usedPercent)
  const worst = used.length ? Math.max(...used) : -1
  const w = worst < 0 ? 'clear' : worst >= 85 ? 'storm' : worst >= 50 ? 'cloudy' : 'clear'
  document.body.dataset.weather = w
  const chip = $('phaseChip')
  if (chip && worst >= 0) chip.title = 'อากาศตาม quota: ใช้ไปสูงสุด ' + Math.round(worst) + '% (' + ({ clear: 'แดดใส', cloudy: 'เมฆมาก', storm: 'พายุ' })[w] + ')'
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
  toast((ci.ok ? '🚀 CI ผ่าน: ' : '💨 CI ไม่ผ่าน: ') + ci.label + ' · ' + ci.who, !ci.ok)
  for (const r of document.querySelectorAll('.rocketItem')) {
    r.classList.remove('launch', 'fizzle')
    void r.offsetWidth
    r.classList.add(ci.ok ? 'launch' : 'fizzle')
    setTimeout(() => r.classList.remove('launch', 'fizzle'), 4200)
  }
  const dep = document.querySelector('.houseHit[data-key="v-deploy"]')
  if (dep && st.board) {
    const x = parseFloat(dep.style.left) + 140
    const y = parseFloat(dep.style.top) + 20
    const rk = img('rocket', x, y, 70, { cls: ci.ok ? 'villageRocket' : 'villageRocket fizzle' })
    st.board.el.append(rk)
    setTimeout(() => rk.remove(), 4300)
  }
}

function drawVillageHouses (b, counts, all) {
  // activity houses: agents of every origin that are in the Deploy / Infra rooms
  const virt = [
    { key: 'v-deploy', room: 'deploy', name: 'บ้าน Deploy', sub: 'กำลัง Build และ Deploy', img: 'house2-pink', x: 110, y: 560, w: 300, icon: 'rocket', tone: 'pink' },
    { key: 'v-ops', room: 'ops', name: 'บ้าน Infra', sub: 'ตรวจสอบสถานะระบบ', img: 'house2-blue', x: 1250, y: 560, w: 290, icon: 'layers', tone: 'blue' }
  ]
  const place = [
    Object.assign({}, houses[0], { img: 'house2-green', x: 160, y: 300, w: 320 }),
    Object.assign({}, houses[1], { img: 'house2-purple', x: 640, y: 250, w: 390 }),
    Object.assign({}, houses[2], { img: 'house2-orange', x: 1170, y: 300, w: 320 })
  ].map(hs => Object.assign(hs, { list: counts[hs.key] })).concat(virt.map(v => Object.assign(v, { list: all.filter(a => a.room === v.room) })))
  for (const hs of place) {
    const list = hs.list
    const busy = list.filter(a => a.mood === 'work').length
    const ask = list.filter(a => a.mood === 'ask').length
    const g = box('houseHit', hs.x, hs.y, hs.w, 0)
    g.dataset.key = hs.key
    g.style.zIndex = hs.y + 200
    g.tabIndex = 0
    g.setAttribute('role', 'button')
    g.setAttribute('aria-label', hs.name + ' — ' + list.length + ' agent')
    g.append(img(hs.img, 0, 0, hs.w, { cls: 'house' + (busy ? ' busy' : '') }))
    const pctBusy = list.length ? Math.round(busy / list.length * 100) : 0
    const card = h('div', { class: 'hcard ' + hs.tone },
      h('div', { class: 'hicon' }, iconSvg(hs.icon)),
      h('div', { class: 'htext' },
        h('b', null, h('i', { class: 'dot' }), hs.name + ' · ' + list.length),
        h('span', null, hs.sub),
        h('div', { class: 'hrow' },
          ask ? h('em', { class: 'pill ask' }, '⏸ ' + ask + ' รอคุณตอบ') : '',
          busy ? h('em', { class: 'pill work' }, '✦ ' + busy + ' กำลังทำงาน') : '',
          !ask && !busy ? h('em', { class: 'pill idle' }, list.length ? 'ว่างทั้งหมด' : 'ไม่มี agent') : '',
          list.length ? h('div', { class: 'pbar' }, h('i')) : '',
          list.length ? h('small', { class: 'ppct' }, pctBusy + '%') : '')),
      h('span', { class: 'pin' }))
    const fill = card.querySelector('.pbar i')
    if (fill) fill.style.width = pctBusy + '%'
    g.append(card)
    const enter = () => {
      stopFollow()
      if (hs.room) {
        // go to the house that has most agents in this room (kcmd if none)
        const byHouse = {}
        for (const a of list) byHouse[a.house] = (byHouse[a.house] || 0) + 1
        st.house = Object.keys(byHouse).sort((x, y) => byHouse[y] - byHouse[x])[0] || 'kcmd'
        st.sel = null; st.inspKey = ''
        openRoom(hs.room)
        return
      }
      st.house = hs.key; st.sel = null; st.inspKey = ''; render()
    }
    g.addEventListener('click', enter)
    g.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); enter() } })
    b.append(g)
  }
}

function iconSvg (kind) {
  const v = document.createElementNS(ns2, 'svg')
  v.setAttribute('viewBox', '0 0 24 24')
  const d = {
    code: 'M8 7l-5 5 5 5M16 7l5 5-5 5',
    term: 'M4 7l5 5-5 5M12 17h8',
    layers: 'M12 3l9 5-9 5-9-5zM3 13l9 5 9-5',
    rocket: 'M12 3c3 2 5 6 4 10l-4 4-4-4c-1-4 1-8 4-10zM9 17l-2 4M15 17l2 4M12 9.5v.1'
  }[kind] || 'M4 12h16'
  v.innerHTML = '<path d="' + d + '" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>'
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
  plan: 'กำหนดเป้าหมาย วางแผนงาน และแบ่งงาน',
  library: 'ค้นคว้า รวบรวมข้อมูล และวิเคราะห์',
  code: 'พัฒนาและปรับปรุงตามแผน',
  test: 'ทดสอบการทำงาน ตรวจสอบคุณภาพ',
  ops: 'ดูแลระบบ สภาพแวดล้อม และทรัพยากร',
  deploy: 'เตรียม Build และ Deploy ขึ้นใช้งาน',
  approval: 'รอคำตอบหรือการยืนยันจากคุณ',
  lounge: 'พักและเตรียมพร้อมสำหรับงานถัดไป'
}
const roomShort = { plan: 'วางแผน', library: 'วิจัย', code: 'พัฒนา', test: 'ทดสอบ', ops: 'ระบบ', deploy: 'Deploy', approval: 'รอคุณตอบ', lounge: 'พัก' }
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
  code: { th: 'ห้องพัฒนา', en: 'Development', bg: 'bg-code', screen: ['scr-robot', 302, 112, 196, '</> Dev Monitor'], clock: [70, 150, 38],
    items: [['plant-big', 6, 330, 74], ['desk-blue2', 64, 296, 180], ['desk-pink2', 310, 296, 180], ['desk-white2', 556, 296, 180], ['cactus', 742, 404, 48]] },
  test: { th: 'ห้องทดสอบ', en: 'Testing', bg: 'bg-test', screen: ['scr-blue', 284, 78, 232, '▶ Test Runner'],
    items: [['filing-blue', 600, 214, 70], ['desk-white2', 70, 290, 180], ['desk-blue2', 300, 290, 180], ['printer2', 640, 310, 116], ['hydrangea', 14, 404, 64]] },
  library: { th: 'ห้องวิจัย', en: 'Research', bg: 'bg-library', screen: ['scr-white', 306, 106, 188, '📖 Research Notes'],
    items: [['shelf-big', 70, 176, 210], ['shelf-tall', 566, 190, 96], ['globe', 680, 296, 56], ['table-meet', 284, 292, 232], ['plant-stand', 30, 380, 66], ['books2', 600, 420, 70]] },
  plan: { th: 'ห้องวางแผน', en: 'Planning', bg: 'bg-plan', screen: ['scr-white', 292, 118, 216, '◷ Plan Board'], clock: [690, 150, 40],
    items: [['desk-boss2', 70, 270, 240], ['table-meet', 450, 296, 226], ['plant-big', 8, 362, 70], ['calendar', 720, 420, 56]] },
  approval: { th: 'รอคุณตอบ', en: 'Waiting for Reply', bg: 'bg-approval', screen: ['scr-bunny', 270, 92, 260, '💬 รอคุณตอบ'],
    items: [['reception2', 34, 312, 260], ['sofa-yellow2', 480, 330, 270], ['water', 734, 268, 58], ['flowers-pot', 330, 420, 52], ['pillow-bunny', 520, 352, 52]] },
  ops: { th: 'ห้องระบบ', en: 'Infrastructure', bg: 'bg-ops', screen: ['scr-robot', 238, 110, 324, '◉ System Monitor'],
    items: [['servers2', 22, 200, 196], ['filing-blue', 700, 262, 66], ['desk-white2', 300, 316, 180], ['printer2', 556, 362, 104], ['plant-big', 722, 380, 70]] },
  deploy: { th: 'ห้อง Deploy', en: 'Deploy', bg: 'bg-deploy', screen: ['scr-lamp', 236, 128, 328, '🚀 Deployment Board'],
    items: [['desk-pink2', 24, 324, 176], ['desk-pink2', 600, 324, 176], ['rug-pink2', 240, 404, 320], ['rocket', 352, 352, 96], ['bean-pink', 266, 404, 84], ['bean-yellow', 452, 404, 84], ['box-pallet', 220, 320, 74], ['handtruck', 528, 316, 54]] },
  lounge: { th: 'ห้องพัก', en: 'Lounge', bg: 'bg-lounge', screen: ['scr-tablet', 312, 104, 176, '☕ TV'], clock: [736, 214, 34],
    items: [['rug-cloud', 196, 418, 250], ['sofa-pink2', 52, 318, 276], ['armchair2', 352, 330, 112], ['hammock2', 494, 318, 236], ['bean-purple', 290, 424, 76], ['water', 742, 296, 52], ['plant-stand', 8, 392, 58]] },
  entrance: { th: 'ทางเข้า', en: 'Entrance', garden: true,
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
  host.append(h('div', { class: 'rlabel2' }, step ? h('em', { class: 'step' }, String(step)) : null, h('b', null, R.th), h('span', null, R.en)))
  return withScreen ? screen : null
}

// NPC guides: one resident per room who explains what the room is for
const npcs = {
  plan: ['cx-kid6', 742, 'สวัสดี! ที่นี่ห้องวางแผน 📋', 'agent ที่กำลังวางแผน แตกงานเป็นขั้น หรือส่งงานให้ sub-agent จะมาอยู่ที่นี่'],
  library: ['cx-kid1', 560, 'ห้องวิจัยค่ะ 🔎', 'agent ที่กำลังอ่านไฟล์ ค้นโค้ด หรือค้นข้อมูลจากเว็บ (Read · Grep · WebSearch) จะมานั่งค้นคว้าที่นี่'],
  code: ['cx-kid0', 754, 'ห้องพัฒนา </>', 'agent ที่กำลังแก้ไฟล์โค้ดจริง (Edit · Write) จะมานั่งโต๊ะคอมที่นี่'],
  test: ['cx-kid4', 556, 'ห้องทดสอบ 🧪', 'agent ที่กำลังรันเทส เช่น go test, bun test, playwright จะมาที่นี่ ผ่านแล้วค่อยไปห้อง Deploy'],
  ops: ['cx-kid5', 650, 'ห้องระบบ ⚙️', 'agent ที่กำลังรันคำสั่งทั่วไปใน shell เช่น ตรวจเครื่อง ดู log จัดการไฟล์ จะมาอยู่ที่นี่'],
  deploy: ['cx-fox', 560, 'ห้อง Deploy 🚀', 'agent ที่กำลัง build / deploy หรือรอ Jenkins · CI อยู่ จะมานั่งที่นี่ จอจะโชว์งาน CI และสถานะล่าสุด'],
  approval: ['cx-kid7', 440, 'ห้องรอคุณตอบ 💬', 'agent ที่ถามคำถามหรือรอให้คุณยืนยัน จะมารอที่นี่ — คลิก agent แล้วตอบได้จากช่องด้านล่าง'],
  lounge: ['cx-panda', 470, 'ห้องพัก ☕', 'agent ที่ทำงานเสร็จแล้วหรือว่างรอคำสั่งใหม่ จะมาพักที่นี่']
}
function npcEl (key, big) {
  const [ch, x, title, text] = npcs[key]
  const n = box('npc', x - 40, 360, 80, 140)
  n.append(img(ch, 0, 0, 80, { cls: 'npcImg' }))
  n.title = title + ' — ' + text
  if (big) {
    const say = h('div', { class: 'npcSay' }, h('b', null, title), h('p', null, text))
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
  banner.append(h('b', null, 'AI Agent Workflow'), h('span', null, 'ลำดับการทำงานของ Agent ตั้งแต่การวางแผนจนถึงพัก'), img('cx-bunny', -36, 6, 70, { cls: 'wfBunny' }))
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
    card.classList.add('zoomable')
    card.title = 'คลิกเพื่อซูมเข้าห้อง · จอมอนิเตอร์ข้อความ'
    card.addEventListener('click', e => { if (!e.target.closest('.actor')) openRoom(key) })
    b.append(card)
    const plq = img('plq-' + key, r.x + 14, r.y - 40, 214, { cls: 'plaque' })
    const stepEl = box('plqStep', r.x - 4, r.y - 30, 34, 34)
    stepEl.textContent = String(r.step)
    b.append(plq, stepEl)
    if (r.col < 3) b.append(img('arr-pill', r.x + CW + 6, r.y + CH / 2 - 20, 40, { cls: 'flowArrow2' }))
  }
  // overview strip: click a step to open that room
  const strip = box('flowStrip', 92, IH - 110, IW - 184, 70)
  strip.append(h('b', { class: 'fsTitle' }, 'ภาพรวมการทำงาน'))
  for (const key of Object.keys(rooms2).sort((x, y) => rooms2[x].step - rooms2[y].step)) {
    strip.append(h('button', { type: 'button', class: 'fsStep s' + rooms2[key].step, onclick: () => openRoom(key) }, h('i', null, String(rooms2[key].step)), roomShort[key]))
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

function charFor (id) { return chars[hash(id) % chars.length] }

function moodAnim (a) {
  if (a.mood === 'work') return a.label === activityLabel.planning || a.label === activityLabel.research ? 'think' : 'work'
  if (a.mood === 'ask') return 'think'
  if (a.label === 'เสร็จแล้ว') return 'cheer'
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
  const fx = a.mood === 'ask' ? fxFor.ask : a.mood === 'lost' ? fxFor.lost : a.label === 'เสร็จแล้ว' ? fxFor.done : ''
  if (sel || a.mood === 'lost') d.append(h('div', { class: 'say ' + a.mood }, a.label))
  else if (fx) d.append(img(fx, 0, 0, 26, { cls: 'fx' }))
  act.spr = h('div', { class: 'spr play' })
  act.anim2 = ''
  d.append(act.spr)
  d.append(h('span', { class: 'rtBadge', title: a.runtime === 'codexCli' ? 'Codex' : 'Claude Code', 'aria-hidden': 'true' }, a.runtime === 'codexCli' ? '>_' : '✻'))
  const nm = a.name.length > 12 ? a.name.slice(0, 11) + '…' : a.name
  d.append(h('div', { class: 'tag' + (a.canTake ? ' reach' : '') + (a.runtime === 'codexCli' ? ' codex' : '') }, a.canTake ? h('i') : null, nm))
  act.mood = moodAnim(a)
  setAnim(act, act.walking ? act.walkDir || 'walkR' : act.mood)
}

// ----- room focus: one room large + a live message monitor -----
const roomTitle = {
  code: 'ห้องพัฒนา', test: 'ห้องทดสอบ', library: 'ห้องวิจัย', plan: 'ห้องบริหาร · วางแผน', approval: 'รอคุณตอบ',
  ops: 'ห้องระบบ', deploy: 'ห้อง Deploy', lounge: 'ห้องพัก', entrance: 'ทางเข้า'
}
const FW = 1640
const FH = 990

function flyTo (kind, id) {
  let houseKey = 'kcmd'
  if (kind === 'session') {
    const sess = st.snap.sessions.find(x => x.sessionId === id)
    if (!sess || !sess.live) return select(kind, id)
    houseKey = originOf(sess)
  }
  const a = actorsFor(houseKey).find(x => x.id === id)
  if (st.view !== 'map') setView('map')
  st.house = houseKey
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
  act.mood = pose === 'work' ? 'work' : pose === 'stand' ? (act.a.mood === 'sleep' && act.a.label === 'เสร็จแล้ว' ? 'cheer' : 'think') : 'rest'
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
  const want = actorsFor(st.house).filter(a => a.room === key)
  const fresh = st.drawn !== 'focus:' + key || !st.focus || !st.focus.stage.isConnected
  if (fresh) {
    st.drawn = 'focus:' + key
    st.actors = {}
    const b = sceneBoard(FW, FH, 'focus')
    const FS = 1500 / RW
    const stageEl = box('focusRoom', 70, 10, 1500, RH * FS)
    const screen = composeRoom(stageEl, key, FS, true)
    if (screen) {
      screen.classList.add('live')
      screen.querySelector('.scrBody').replaceChildren(h('p', { class: 'scrNote' }, want.length ? 'กำลังโหลดข้อความ…' : 'ไม่มี agent ในห้องนี้'))
      const tv = screen.closest('.screenWrap') || screen
      tv.classList.add('tvLive')
      tv.title = 'คลิกเพื่อขยายจอ'
      tv.setAttribute('role', 'button')
      tv.tabIndex = 0
      tv.append(h('span', { class: 'zoomBadge' }, '⤢ ขยาย'))
      tv.addEventListener('click', () => openScreenPop(key))
      tv.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openScreenPop(key) } })
    }
    b.append(stageEl)
    b.append(box('focusHint', 70, RH * FS + 26, 1500, 0))
    b.querySelector('.focusHint').textContent = 'ลาก agent ไปวางที่โซฟา เก้าอี้ เปล หรือจุดไหนก็ได้ · คลิก agent เพื่อเปิดแชต · คลิกจอเพื่อขยาย · ← หรือ Esc กลับ'
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
    walk(act, [[doorSpot[0] * F.FS, doorSpot[1] * F.FS]], () => { act.el.remove(); delete F.actors[id] })
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
    if (!act.dragging && Math.hypot(e.clientX - start.x, e.clientY - start.y) < 6) return
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
  { key: 'night', from: 0, label: 'ดึก', icon: '🌙' },
  { key: 'morning', from: 6, label: 'เช้า', icon: '🌅' },
  { key: 'late', from: 9, label: 'สาย', icon: '🌤' },
  { key: 'afternoon', from: 12, label: 'บ่าย', icon: '☀️' },
  { key: 'evening', from: 16, label: 'เย็น', icon: '🌇' },
  { key: 'night', from: 19, label: 'ดึก', icon: '🌙' }
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
  if (!want.length) return [h('p', { class: 'scrNote' }, 'ไม่มี agent ในห้องนี้')]
  return want.map(a => h('div', big ? { class: 'scrItem ' + a.mood, tabindex: 0, onclick: e => { e.stopPropagation(); $('screenPop').close(); st.tab = 'chat'; select(a.kind, a.id) } } : { class: 'scrItem ' + a.mood },
    h('div', { class: 'scrTop' }, h('b', null, a.name), h('em', null, a.kind === 'sub' ? 'sub-agent' : a.label)),
    ...(big && a.kind === 'session' ? openReviewsOf(a.id).map(r => h('button', { type: 'button', class: 'pri revOpen', onclick: e => { e.stopPropagation(); $('screenPop').close(); openReview(r.id) } }, '🧪 เปิดตรวจงาน: ' + r.title)) : []),
    ...((a.watches || []).filter(w => w.ci).slice(0, big ? 6 : 2).map(w => h('div', { class: 'scrJob' }, h('i'), h('b', null, w.label), h('span', null, (w.event || 'รอผล…') + (w.eventAt && !w.eventAt.startsWith('0001') ? ' · ' + fmtTime(w.eventAt) : ''))))),
    ...((msgsById[a.id] || []).slice(big ? -3 : -1).map(m => h('div', { class: 'scrMsg ' + m.role }, h('span', null, (m.role === 'user' ? 'ผู้ใช้' : 'agent') + ' · ' + fmtTime(m.at)), h('p', null, big ? m.text : m.text.slice(0, 160)))))))
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
  if (body) body.replaceChildren(...monitorCards(want, st.monMsgs, false))
  if ($('screenPop').open && st.popRoom === st.roomFocus) $('screenPopBody').replaceChildren(...popContent(st.popRoom, want))
}

// popContent: the Testing room's screen always starts with the reviews
function popContent (key, want) {
  return [...(key === 'test' ? [reviewList(true)] : []), ...monitorCards(want, st.monMsgs || {}, true)]
}

function openScreenPop (key) {
  st.popRoom = key
  const R = roomDefs[key]
  const t = R.screen ? R.screen[4] : ''
  $('screenPopTitle').textContent = t.includes(R.th) ? t : t + ' · ' + R.th
  const want = actorsFor(st.house).filter(a => a.room === key)
  $('screenPopBody').replaceChildren(...popContent(key, want))
  $('screenPop').showModal()
}


function renderInterior () {
  const fresh = st.drawn !== 'house:' + st.house || !st.actorLayer || !st.actorLayer.isConnected
  if (fresh) {
    st.drawn = 'house:' + st.house
    st.actors = {}
    st.actorLayer = drawInterior()
  }
  const layer = st.actorLayer
  const want = actorsFor(st.house)
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

function place (act) {
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
function renderList () {
  const box = $('list')
  const rowS = sess => h('tr', { tabindex: 0, onclick: () => select('session', sess.sessionId), onkeydown: e => { if (e.key === 'Enter') select('session', sess.sessionId) } },
    h('td', null, sess.displayName),
    h('td', null, runtimeName[sess.runtimeType]),
    h('td', null, sess.live ? (sess.status === 'idle' ? 'ว่าง' : activityLabel[sess.activityType]) : statusLabel.ended),
    h('td', null, sess.title || '—'),
    h('td', null, ago(sess.lastEventAt)))
  const rowR = run => h('tr', { tabindex: 0, onclick: () => select('run', run.id), onkeydown: e => { if (e.key === 'Enter') select('run', run.id) } },
    h('td', { class: 'mono' }, run.id),
    h('td', null, run.workspace),
    h('td', null, run.lost ? 'ขาดการเชื่อมต่อ' : (runLabel[run.status] || run.status)),
    h('td', null, run.task.slice(0, 120)))
  const head = (...cols) => h('thead', null, h('tr', null, cols.map(c => h('th', null, c))))
  const live = st.snap.sessions.filter(x => x.live)
  const ended = st.snap.sessions.filter(x => !x.live)
  box.replaceChildren(
    h('h3', null, 'Agent ที่ออนไลน์ (' + live.length + ')'),
    h('table', null, head('ชื่อ', 'ชนิด', 'สถานะ', 'งาน', 'ล่าสุด'), h('tbody', null, live.map(rowS))),
    h('h3', null, 'งาน kcmd (' + st.snap.runs.length + ')'),
    h('table', null, head('Run', 'Workspace', 'สถานะ', 'งาน'), h('tbody', null, st.snap.runs.map(rowR))),
    h('h3', null, 'Session ที่จบแล้ววันนี้ (' + ended.length + ')'),
    h('table', null, head('ชื่อ', 'ชนิด', 'สถานะ', 'งาน', 'ล่าสุด'), h('tbody', null, ended.map(rowS)))
  )
}

// ---------- inspector ----------
function targetFor (kind, id) {
  st.blockReason = ''
  if (kind === 'session') {
    const sess = st.snap.sessions.find(x => x.sessionId === id)
    if (sess && sess.dispatchVia) return sess.dispatchVia
    st.blockReason = (sess ? sess.displayName : 'agent นี้') + ' ไม่ได้รันใน tmux ของ kcmd — รับงานจากเว็บไม่ได้ (ดูได้อย่างเดียว)'
    return ''
  }
  const run = st.snap.runs.find(x => x.id === id)
  const w = run && st.snap.workspaces.find(x => x.name === run.workspace)
  return w && w.agentReady ? w.name : ''
}

async function select (kind, id) {
  if (!st.panelOpen) setPanel(true)
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
      const key = [sess.sessionId, tab, sess.status, sess.activityType, sess.messageVia, sess.stopVia, sess.live].join('|')
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
    h('button', { type: 'button', class: 'ghost', onclick: back }, '← ภาพรวม'),
    h('h3', null, 'Sub-agent · ดูได้อย่างเดียว'),
    h('h2', null, sa.description),
    h('dl', { class: 'kv' },
      h('dt', null, 'ชนิด'), h('dd', null, sa.type || '—'),
      h('dt', null, 'สถานะ'), h('dd', null, sa.status === 'done' ? 'เสร็จแล้ว' : 'กำลังทำ · ' + (activityLabel[sa.activityType] || sa.activityType)),
      h('dt', null, 'เครื่องมือล่าสุด'), h('dd', null, sa.lastTool || '—'),
      h('dt', null, 'อัปเดต'), h('dd', null, ago(sa.lastEventAt)),
      h('dt', null, 'agent แม่'), h('dd', null, parent.displayName)
    ),
    sa.task ? h('div', { class: 'subBlock' }, h('h3', null, 'งานที่ได้รับจาก agent แม่'), h('p', null, sa.task)) : null,
    sa.latest ? h('div', { class: 'subBlock' }, h('h3', null, 'รายงานล่าสุด'), h('p', null, sa.latest)) : null,
    h('p', { class: 'note' }, 'sub-agent ทำงานแทน agent แม่ และรายงานผลกลับไปที่ agent แม่ — สั่งงานแทรกไม่ได้ ถ้าจะสั่งให้พิมพ์ถึง agent แม่'),
    h('button', { type: 'button', class: 'pri', onclick: () => { st.tab = 'chat'; select('session', parent.sessionId) } }, 'ไปที่ agent แม่: ' + parent.displayName)
  ]
}

function subCards (sess) {
  const subs = sess.subAgents || []
  if (!subs.length) return null
  return h('div', { class: 'subCards' }, subs.map(sa => h('button', { type: 'button', class: 'subCard ' + (sa.status === 'done' ? 'done' : 'run'), onclick: () => select('sub', sess.sessionId + ':' + sa.id) },
    h('div', { class: 'subTop' }, h('i'), h('b', null, sa.description || sa.type), h('em', null, sa.status === 'done' ? 'เสร็จแล้ว' : (activityLabel[sa.activityType] || 'กำลังทำ') + (sa.lastTool ? ' · ' + sa.lastTool : ''))),
    sa.type ? h('small', null, sa.type + ' · อัปเดต ' + ago(sa.lastEventAt)) : null,
    sa.task ? h('p', null, h('u', null, 'งาน: '), sa.task) : null,
    sa.latest ? h('p', { class: 'latest' }, h('u', null, 'ล่าสุด: '), sa.latest) : null)))
}

function subList (sess) {
  const subs = sess.subAgents || []
  if (!subs.length) return null
  return h('div', { class: 'tl' }, h('h3', null, 'Sub-agents ที่ช่วยอยู่ (' + subs.length + ') · ดูได้อย่างเดียว'),
    subs.map(sa => h('div', null, h('span', null, sa.status === 'done' ? '✓' : '●'), h('span', null, (sa.type ? sa.type + ' · ' : '') + sa.description + (sa.lastTool ? ' — ' + sa.lastTool : '')))))
}

function sessionHeader (sess) {
  const ava = h('div', { class: 'ava' }, avatarSvg(agentHex(sess)))
  ava.style.background = 'color-mix(in srgb, ' + agentColor(sess) + ' 18%, transparent)'
  const stateDot = h('i')
  stateDot.style.background = !sess.live ? 'var(--line)' : sess.status === 'running' ? 'var(--brand)' : sess.status === 'needsInput' ? 'var(--amber)' : 'var(--muted)'
  const where = sess.messageVia === 'kcmd' ? 'บ้าน kcmd' : sess.kind === 'bg' ? 'บ้าน Background' : 'บ้าน Terminal · VS Code'
  return [
    h('button', { type: 'button', class: 'ghost', onclick: back }, '← ภาพรวม'),
    h('div', { class: 'who' }, ava, h('div', null, h('h2', null, sess.displayName), h('p', null, runtimeName[sess.runtimeType] + ' · ' + where + ' · ' + (sess.repoId || '—')))),
    h('div', { class: 'state' }, stateDot, stateText(sess), (sess.subAgents || []).length
      ? h('button', { type: 'button', class: 'pill work subToggle' + (st.subsOpen ? ' on' : ''), 'aria-expanded': String(!!st.subsOpen), onclick: () => { st.subsOpen = !st.subsOpen; st.inspKey = ''; renderInspector() } }, '+' + sess.subAgents.length + ' sub-agent ' + (st.subsOpen ? '▴' : '▾'))
      : null),
    st.subsOpen ? subCards(sess) : null,
    ...openReviewsOf(sess.sessionId).map(r => h('button', { type: 'button', class: 'revAlert', onclick: () => openReview(r.id) }, h('b', null, '🧪 รอคุณตรวจงาน'), h('span', null, r.title), h('em', null, 'เปิดตรวจ →'))),
    modelRow(sess),
    sess.doing && sess.live && sess.status === 'running' ? h('div', { class: 'doing' }, h('i'), h('span', null, sess.doing.detail), h('small', null, ago(sess.doing.at))) : null,
    h('div', { class: 'tabs', role: 'tablist' },
      h('button', { type: 'button', role: 'tab', class: (st.tab || 'chat') === 'chat' ? 'on' : '', 'aria-selected': String((st.tab || 'chat') === 'chat'), onclick: () => { st.tab = 'chat'; st.inspKey = ''; renderInspector() } }, 'บทสนทนา'),
      h('button', { type: 'button', role: 'tab', class: st.tab === 'detail' ? 'on' : '', 'aria-selected': String(st.tab === 'detail'), onclick: () => { st.tab = 'detail'; st.inspKey = ''; renderInspector() } }, 'รายละเอียด'))
  ]
}

function sessionPanel (sess, tab) {
  if (tab === 'chat') return [...sessionHeader(sess), chatPanel(sess)]
  const ws = sess.dispatchVia && st.snap.workspaces.find(w => w.name === sess.dispatchVia)
  const usage = sess.usage || {}
  let tokens = 'ไม่ได้นับ'
  if (usage.source !== 'notCounted') tokens = num(usage.inputTokens) + ' เข้า · ' + num(usage.outputTokens) + ' ออก · cache ' + num(usage.cachedTokens) + (usage.complete ? '' : ' (กำลังนับ… ยังไม่ครบ)')
  const tl = (st.detail && st.detail.session.sessionId === sess.sessionId) ? st.detail.timeline : null
  const reach = sess.messageVia === 'kcmd'
    ? 'ได้ — ผ่าน kcmd workspace ' + sess.dispatchVia + (ws ? ' (tmux ' + ws.tmuxSession + ')' : '')
    : sess.messageVia === 'relay' ? 'ได้ — ผ่าน relay (tmux aiagent-' + sess.sessionId.slice(0, 8) + ' ที่รัน claude attach)' : 'ไม่ได้ — ' + (sess.messageBlocked || 'ไม่มีช่องทางที่ปลอดภัย')
  return [
    ...sessionHeader(sess),
    sess.title ? h('p', null, sess.title) : null,
    h('dl', { class: 'kv' },
      h('dt', null, 'Session'), h('dd', { class: 'mono', title: sess.sessionId }, shortId(sess.sessionId)),
      h('dt', null, 'เจ้าของ'), h('dd', null, sess.owner),
      h('dt', null, 'โฟลเดอร์'), h('dd', { class: 'mono' }, sess.cwd || '—'),
      h('dt', null, 'Branch'), h('dd', { class: 'mono' }, sess.gitBranch || '—'),
      h('dt', null, 'เริ่ม'), h('dd', null, sess.startedAt ? fmtTime(sess.startedAt) + ' · ' + ago(sess.startedAt) : '—'),
      h('dt', null, 'เหตุการณ์ล่าสุด'), h('dd', null, ago(sess.lastEventAt)),
      h('dt', null, 'หลักฐาน'), h('dd', null, (evidenceLabel[sess.liveEvidence] || sess.liveEvidence) + (sess.pid ? ' · pid ' + sess.pid : '')),
      h('dt', null, 'ส่งข้อความจากเว็บ'), h('dd', null, reach),
      h('dt', null, 'ดูในเครื่อง'), h('dd', null, localCmd(sess)),
      h('dt', null, 'Tokens'), h('dd', null, tokens),
      h('dt', null, 'เครื่องมือล่าสุด'), h('dd', null, sess.lastTool || '—')
    ),
    sess.live && sess.stopVia
      ? h('button', { type: 'button', class: 'warn', onclick: () => doStop(sess) }, sess.stopVia === 'claudeStop' ? 'หยุด agent (claude stop)' : 'หยุด agent (kcmd restart)')
      : (sess.live && sess.stopBlocked ? h('p', { class: 'note' }, 'หยุดจากเว็บไม่ได้: ' + sess.stopBlocked) : null),
    sess.prs && sess.prs.length ? h('div', { class: 'btns' }, sess.prs.map(pr => h('a', { href: pr.url, target: '_blank', rel: 'noopener noreferrer', class: 'pill' }, 'PR #' + pr.number + ' · ' + pr.repository))) : null,
    subList(sess),
    tl ? h('div', { class: 'tl' }, h('h3', null, 'ลำดับงานล่าสุด (ชื่อเครื่องมือเท่านั้น)'), tl.slice().reverse().slice(0, 14).map(t => h('div', null, h('span', null, fmtTime(t.at)), h('span', null, (activityLabel[t.activityType] || t.activityType) + ' · ' + t.tool)))) : h('p', { class: 'note' }, 'กำลังโหลดลำดับงาน…')
  ]
}

function localCmd (sess) {
  const cmd = sess.dispatchVia ? 'kcmd attach ' + sess.dispatchVia : sess.kind === 'bg' ? 'claude attach ' + sess.sessionId.slice(0, 8) : ''
  if (!cmd) return 'เปิดอยู่ในเทอร์มินัล/VS Code ของผู้ใช้'
  return h('span', { class: 'cmd' }, h('code', null, cmd), h('button', { type: 'button', class: 'ghost copy', onclick: e => { copyText(cmd, e.currentTarget) } }, 'คัดลอก'))
}

function copyText (text, btn) {
  const done = () => { btn.textContent = 'คัดลอกแล้ว'; setTimeout(() => { btn.textContent = 'คัดลอก' }, 1500) }
  try {
    navigator.clipboard.writeText(text).then(done, () => { btn.textContent = text })
  } catch (e) { btn.textContent = text }
}

function chatPanel (sess) {
  const log = h('div', { class: 'chatLog', 'aria-live': 'polite' }, h('p', { class: 'note' }, 'กำลังโหลดบทสนทนา…'))
  const parts = [log]
  parts.push(h('p', { class: 'note' }, sess.messageVia ? 'พิมพ์ตอบที่ช่องยาวด้านล่าง (ส่งถึง agent ตัวนี้) · แนบรูปได้ · ' + (sess.messageVia === 'relay' ? 'ผ่าน relay claude attach ' + sess.sessionId.slice(0, 8) : 'ผ่าน kcmd dispatch ' + sess.dispatchVia) : 'ส่งข้อความจากเว็บไม่ได้: ' + (sess.messageBlocked || 'ไม่มีช่องทางที่ปลอดภัย')))
  parts.push(h('p', { class: 'note' }, 'แสดงเฉพาะข้อความผู้ใช้และคำตอบของ agent · ข้อมูลลับ (token, key, รหัสผ่าน) ถูกปิดบังก่อนส่งมาที่เว็บ'))
  return h('div', { class: 'chat' }, parts)
}

async function updateChat (force) {
  const sel = st.sel
  if (!sel || sel.kind !== 'session') return
  const id = sel.id
  try {
    const res = await api('/sessions/' + encodeURIComponent(id) + '/messages?limit=60')
    if (!st.sel || st.sel.id !== id) return
    const msgs = res.messages || []
    const sig = msgs.length + ':' + (msgs.length ? msgs[msgs.length - 1].at + msgs[msgs.length - 1].text.length : '')
    const log = $('insp').querySelector('.chatLog')
    if (!log || (!force && st.chatSig === sig)) return
    st.chatSig = sig
    const nearBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 80
    log.replaceChildren(...(msgs.length ? msgs.map(m => h('div', { class: 'msg ' + m.role },
      h('div', { class: 'who2' }, (m.role === 'user' ? 'ผู้ใช้' : 'agent') + ' · ' + fmtTime(m.at)),
      h('div', { class: 'body' }, m.text), msgImages(m.text))) : [h('p', { class: 'note' }, 'ยังไม่มีข้อความ')]))
    if (force || nearBottom) log.scrollTop = log.scrollHeight
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
    if (log && e.message !== 'auth') log.replaceChildren(h('p', { class: 'note' }, 'โหลดบทสนทนาไม่ได้: ' + e.message))
  }
}

// attachments take a fixed-size stack in the row; the full list opens as a
// tray above the composer so the text box never shrinks
function drawThumbs (thumbs) {
  const list = st.attach || []
  if (!list.length) { st.trayOpen = false; return thumbs.replaceChildren() }
  const stack = h('button', { type: 'button', class: 'attStack', 'aria-expanded': String(!!st.trayOpen), title: 'ดู/จัดการรูปแนบ', onclick: () => { st.trayOpen = !st.trayOpen; drawThumbs(thumbs) } },
    ...list.slice(-3).map(a => h('img', { src: a.url, alt: '' })), h('b', null, String(list.length)))
  const tray = st.trayOpen ? h('div', { class: 'attTray', role: 'dialog', 'aria-label': 'รูปแนบ' },
    h('div', { class: 'trayHead' }, h('b', null, 'รูปแนบ ' + list.length + '/10'), h('span', null, 'คลิกรูปเพื่อดูใหญ่'),
      list.length < 10 ? h('button', { type: 'button', class: 'ghost', onclick: () => $('askFile').click() }, '+ เพิ่มรูป') : null,
      h('button', { type: 'button', class: 'ghost', 'aria-label': 'ปิด', onclick: () => { st.trayOpen = false; drawThumbs(thumbs) } }, '✕')),
    h('div', { class: 'trayGrid' }, list.map((a, i) => h('div', { class: 'thumb' },
      h('img', { src: a.url, alt: 'รูปแนบ ' + (i + 1), title: 'คลิกเพื่อดูภาพใหญ่', onclick: () => openLightbox(a.url) }),
      h('button', { type: 'button', 'aria-label': 'ลบรูป ' + (i + 1), onclick: () => { URL.revokeObjectURL(a.url); st.attach.splice(i, 1); drawThumbs(thumbs) } }, '×'))))) : null
  thumbs.replaceChildren(stack, tray || '')
}
document.addEventListener('click', e => {
  if (st.trayOpen && e.target.isConnected && !e.target.closest('#askThumbs') && !e.target.closest('#lightbox') && !e.target.closest('#askClip')) { st.trayOpen = false; drawThumbs($('askThumbs')) }
})

async function shrink (blob) {
  if (blob.size <= 3400000) return blob
  const bmp = await createImageBitmap(blob)
  const k = Math.sqrt(3000000 / blob.size)
  const c = document.createElement('canvas')
  c.width = Math.max(1, Math.round(bmp.width * k))
  c.height = Math.max(1, Math.round(bmp.height * k))
  c.getContext('2d').drawImage(bmp, 0, 0, c.width, c.height)
  return new Promise(resolve => c.toBlob(resolve, 'image/jpeg', 0.85))
}

async function uploadImage (blob) {
  const body = await shrink(blob)
  const res = await fetch('/aiagent/api/uploads', { method: 'POST', headers: { 'X-Aiagent-Token': st.token, 'Content-Type': body.type || 'application/octet-stream' }, body, cache: 'no-store' })
  let j = null
  try { j = await res.json() } catch (e) { j = null }
  if (!res.ok) throw new Error((j && j.message) || ('HTTP ' + res.status))
  return j.details.path
}

const uploadRef = /\/uploads\/([0-9]{8}-[0-9]{6}-[0-9a-f]{16}\.(?:png|jpg|gif|webp))/g
st.imgCache = {}
function msgImages (text) {
  const names = [...text.matchAll(uploadRef)].map(m => m[1])
  if (!names.length) return null
  const wrap = h('div', { class: 'msgImgs' })
  for (const n of names.slice(0, 10)) {
    const im = h('img', { alt: n, title: 'คลิกเพื่อดูภาพใหญ่' })
    im.addEventListener('click', () => { if (im.src) openLightbox(im.src) })
    wrap.append(im)
    loadUpload(n).then(u => { if (u) im.src = u }, () => {})
  }
  return wrap
}
async function loadUpload (name) {
  if (st.imgCache[name]) return st.imgCache[name]
  const res = await fetch('/aiagent/api/uploads/' + encodeURIComponent(name), { headers: { 'X-Aiagent-Token': st.token } })
  if (!res.ok) return ''
  st.imgCache[name] = URL.createObjectURL(await res.blob())
  return st.imgCache[name]
}

async function sendChat (sess, ta, thumbs) {
  let text = expandPastes(ta.value).trim()
  const files = st.attach || []
  if (!text && !files.length) return
  st.confirmedChat = st.confirmedChat || {}
  if (!st.confirmedChat[sess.sessionId]) {
    const how = sess.messageVia === 'relay' ? 'บริการนี้จะเปิด claude attach ' + sess.sessionId.slice(0, 8) + ' ใน tmux ส่วนตัวแล้วพิมพ์ข้อความนี้ให้ (เหมือนคุณพิมพ์เอง)' : 'ส่งผ่าน kcmd dispatch ' + sess.dispatchVia
    if (!await confirmBox('ส่งข้อความถึง ' + sess.displayName, how + ' · ยืนยันครั้งเดียวต่อ agent ในแท็บนี้', text + (files.length ? '\n\n+ รูปแนบ ' + files.length + ' รูป' : ''), 'ส่ง')) return
    st.confirmedChat[sess.sessionId] = true
  }
  ta.disabled = true
  if (files.length) {
    try {
      const paths = []
      for (const f of files) paths.push(await uploadImage(f.blob))
      text = (text || 'ดูรูปที่แนบ') + '\n\n' + paths.map((p, i) => 'รูปแนบ ' + (i + 1) + ': ' + p).join('\n')
    } catch (e) {
      ta.disabled = false
      toast('อัปโหลดรูปไม่สำเร็จ: ' + e.message, true)
      return
    }
  }
  const ok = await act('/sessions/' + encodeURIComponent(sess.sessionId) + '/message', { message: text }, r => 'ส่งถึง ' + sess.displayName + ' แล้ว (' + (r.via || '') + ')' + (files.length ? ' พร้อมรูป ' + files.length + ' รูป' : ''))
  ta.disabled = false
  if (ok) {
    ta.value = ''
    clearPastes()
    st.chatDraft = null
    for (const f of files) URL.revokeObjectURL(f.url)
    st.attach = []
    if (thumbs) drawThumbs(thumbs)
    growText()
    setTimeout(() => updateChat(true), 1500)
  }
  ta.focus()
}

function runPanel (run) {
  const waiting = run.status === 'WAITING_DECISION'
  const done = ['DONE', 'ABORTED', 'ARCHIVED'].includes(run.status)
  const answerInput = h('input', { maxlength: 4000, placeholder: 'พิมพ์คำตอบ…', 'aria-label': 'คำตอบ' })
  return [
    h('button', { type: 'button', class: 'ghost', onclick: back }, '← ภาพรวม'),
    h('h3', { class: 'mono' }, run.id + ' · ' + run.workspace),
    h('h2', null, run.lost ? 'ขาดการเชื่อมต่อ' : (runLabel[run.status] || run.status)),
    h('p', null, run.task),
    h('dl', { class: 'kv' },
      h('dt', null, 'tmux'), h('dd', null, run.tmuxSession + (run.tmuxAlive ? ' · เปิดอยู่' : ' · ไม่พบ session')),
      h('dt', null, 'สร้าง'), h('dd', null, fmtTime(run.createdAt) + ' · ' + ago(run.createdAt)),
      h('dt', null, 'อัปเดต'), h('dd', null, ago(run.updatedAt)),
      h('dt', null, 'ที่มาของสถานะ'), h('dd', null, 'agent/ผู้ใช้รายงานผ่าน kcmd (ไม่ได้วัดจาก process)')
    ),
    waiting && run.decision ? h('div', { class: 'card warn' },
      h('b', null, run.decision.question),
      h('div', { class: 'btns' }, (run.decision.options || []).map(o => h('button', { type: 'button', onclick: () => doAnswer(run, o) }, o))),
      h('div', { class: 'answer' }, answerInput, h('button', { type: 'button', class: 'pri', onclick: () => doAnswer(run, answerInput.value) }, 'ตอบ'))
    ) : null,
    done ? null : h('div', { class: 'btns' },
      h('button', { type: 'button', class: 'pri', onclick: () => doRunStatus(run, 'approve') }, 'อนุมัติ (ตั้งเป็น READY)'),
      h('button', { type: 'button', class: 'warn', onclick: () => doRunStatus(run, 'abort') }, 'ยกเลิกงาน')
    ),
    h('p', { class: 'note' }, 'อนุมัติ/ยกเลิก เปลี่ยนสถานะใน kcmd เท่านั้น — ไม่ merge, ไม่ deploy และไม่ปิด process'),
    h('h3', null, 'บันทึกของงาน'),
    h('div', { class: 'log mono' }, st.runLog ? (st.runLog.join('\n') || '(ว่าง)') : 'กำลังโหลด…')
  ]
}

function overviewPanel () {
  const snap = st.snap
  const needs = []
  for (const sess of snap.sessions) {
    if (sess.status === 'needsInput') needs.push(h('div', { class: 'card warn', tabindex: 0, onclick: () => { st.tab = 'chat'; flyTo('session', sess.sessionId) } }, h('b', null, sess.displayName + ' รอคุณตอบ'), h('small', null, (sess.messageVia ? 'ตอบจากเว็บได้ · ' : 'ดูได้อย่างเดียว · ') + ago(sess.lastEventAt))))
  }
  for (const run of snap.runs) {
    if (run.status === 'WAITING_DECISION') needs.push(h('div', { class: 'card warn', tabindex: 0, onclick: () => select('run', run.id) }, h('b', null, (run.decision && run.decision.question) || run.task.slice(0, 120)), h('small', null, run.id + ' · ' + run.workspace + ' · ' + ago(run.updatedAt))))
    else if (run.lost) needs.push(h('div', { class: 'card bad', tabindex: 0, onclick: () => select('run', run.id) }, h('b', null, run.id + ' ขาดการเชื่อมต่อ'), h('small', null, 'สถานะ RUNNING แต่ไม่พบ tmux ' + run.tmuxSession)))
  }
  const quotas = snap.quotas.map(q => {
    const fill = h('div', { class: 'fill' })
    if (q.confidence === 'actual') {
      fill.style.width = Math.max(0, Math.min(100, q.remainingPercent)) + '%'
      fill.style.background = q.mode === 'normal' ? 'var(--brand)' : q.mode === 'reserve' ? 'var(--rose)' : 'var(--amber)'
    } else {
      fill.style.width = '0'
    }
    const right = q.confidence === 'actual' ? 'ใช้ไป ' + Math.round(q.usedPercent) + '%' : (q.confidence === 'stale' ? 'รีเซ็ตแล้ว' : 'ไม่ทราบ')
    const note = q.confidence === 'actual'
      ? 'รอบ' + windowName(q.windowMinutes) + (q.resetsAt ? ' · รีเซ็ต ' + new Date(q.resetsAt).toLocaleString('th-TH', { dateStyle: 'short', timeStyle: 'short' }) : '') + ' · อ่านเมื่อ ' + ago(q.observedAt)
      : q.source
    return h('div', { class: 'quota' }, h('span', null, (providerName[q.providerId] || q.providerId) + (q.planType ? ' · ' + q.planType : '')), h('b', null, right), h('div', { class: 'track' }, fill), h('small', { class: 'note' }, note))
  })
  return [
    h('h2', null, 'ภาพรวมวันนี้'),
    h('h3', null, 'ต้องให้คุณช่วย (' + needs.length + ')'),
    needs.length ? needs : h('p', { class: 'note' }, 'ไม่มีงานที่รอคุณ'),
    reviewList(false),
    h('h3', null, 'พลังงาน AI'),
    quotas,
    quotaAdvice(),
    policySlot(),
    h('p', { class: 'note' }, 'ตัวเลข quota มาจากผู้ให้บริการเท่านั้น ถ้าไม่มีข้อมูลจะแสดง “ไม่ทราบ” ไม่ประมาณเอง'),
    h('h3', null, 'วิธีใช้'),
    h('p', { class: 'note' }, 'คลิกตัวละครหรือแถวในมุมมองรายการเพื่อดูรายละเอียด · ช่องด้านล่างใช้สั่งงานไปยัง agent ใน workspace ของ kcmd')
  ]
}

// quotaAdvice: how to get a Claude reading, or which agents to move to a
// cheaper model when the plan window is running low
function quotaAdvice () {
  const qs = st.snap.quotas.filter(q => q.providerId === 'anthropic')
  if (qs.every(q => q.confidence === 'unknown')) {
    return h('div', { class: 'advice' }, h('b', null, 'เปิดการอ่าน % ของ Claude'),
      h('p', null, 'Claude Code ส่งตัวเลข % ให้เฉพาะ status line — ตั้งครั้งเดียวใน ~/.claude/settings.json:'),
      h('code', null, '"statusLine": {"type": "command", "command": "aiagent statusline"}'),
      h('p', { class: 'note' }, 'ตัวเลขอัปเดตเมื่อมี Claude session ที่เปิดหน้าจออยู่อย่างน้อยหนึ่งตัว'))
  }
  const worst = qs.filter(q => q.confidence === 'actual').sort((a, b) => b.usedPercent - a.usedPercent)[0]
  if (!worst || worst.usedPercent < 60) return null
  const heavy = st.snap.sessions.filter(x => x.live && x.runtimeType === 'claudeCode' && /opus|fable/i.test(x.model || '') && x.messageVia)
  return h('div', { class: 'advice warn' }, h('b', null, 'Claude ใช้ไป ' + Math.round(worst.usedPercent) + '% (' + windowName(worst.windowMinutes) + ')'),
    h('p', null, heavy.length ? 'แนะนำลด agent ที่ใช้ ' + modelName('opus') + ' เป็น Sonnet เพื่อยืดโควตา:' : 'ไม่มี agent ที่สั่งได้ซึ่งใช้ model ใหญ่อยู่'),
    ...heavy.map(x => h('button', { type: 'button', class: 'ghost', onclick: () => switchModel(x, 'sonnet') }, x.displayName + ' → Sonnet')))
}

function modelName (id) {
  if (!id) return 'ไม่ทราบ'
  const m = /^claude-([a-z]+)-(\d+)(?:-(\d+))?/.exec(id)
  if (m) return m[1][0].toUpperCase() + m[1].slice(1) + ' ' + m[2] + (m[3] && m[3].length < 3 ? '.' + m[3] : '')
  if (/^[a-z]+$/.test(id)) return id[0].toUpperCase() + id.slice(1)
  return id
}

async function switchModel (sess, alias) {
  const ok = await confirmBox('เปลี่ยน model', 'จะพิมพ์ /model ' + alias + ' เข้า ' + sess.displayName + ' (' + (sess.messageVia === 'kcmd' ? 'kcmd ' + sess.dispatchVia : 'relay') + ') — มีผลกับข้อความถัดไปของ agent ตัวนี้', 'ปัจจุบัน: ' + modelName(sess.model) + ' → ' + modelName(alias), 'เปลี่ยนเลย')
  if (!ok) return
  await act('/sessions/' + encodeURIComponent(sess.sessionId) + '/model', { model: alias }, () => 'เปลี่ยน ' + sess.displayName + ' เป็น ' + modelName(alias) + ' แล้ว')
}

async function loadModels () {
  if (st.models && Date.now() - st.models.at < 60000) return st.models
  try { st.models = Object.assign({ at: Date.now() }, await api('/models')) } catch (e) { st.models = st.models || { claude: [], codex: [], at: 0 } }
  return st.models
}

function modelRow (sess) {
  const row = h('div', { class: 'modelRow' }, h('span', { class: 'modelChip', title: sess.model || '' }, '🧠 ' + modelName(sess.model)))
  const codex = sess.runtimeType === 'codexCli'
  const can = sess.live && !codex && sess.messageVia
  loadModels().then(ms => {
    if (codex) {
      row.append(h('select', {
        'aria-label': 'model เริ่มต้นของ Codex',
        onchange: async e => {
          const v = e.target.value
          e.target.value = ''
          if (!v || !await confirmBox('เปลี่ยน model เริ่มต้นของ Codex', 'Codex ที่รันอยู่เปลี่ยนจากภายนอกไม่ได้ — ค่านี้มีผลกับ session ใหม่ (~/.codex/config.toml)', 'เป็น ' + v, 'เปลี่ยน')) return
          await act('/codex/model', { model: v }, () => 'Codex session ใหม่จะใช้ ' + v)
          st.models = null
        }
      }, h('option', { value: '' }, 'model เริ่มต้น: ' + (ms.codexDefault || '—')), ...(ms.codex || []).filter(m => m.id !== ms.codexDefault).map(m => h('option', { value: m.id }, m.label))))
    } else if (can) {
      const cur = (sess.model || '').toLowerCase()
      row.append(h('select', { 'aria-label': 'เปลี่ยน model', onchange: e => { const v = e.target.value; e.target.value = ''; if (v) switchModel(sess, v) } },
        h('option', { value: '' }, 'เปลี่ยน model…'),
        ...(ms.claude || []).filter(m => m.id !== cur).map(m => h('option', { value: m.id }, m.label))))
    } else {
      row.append(h('small', { class: 'note' }, 'เปลี่ยนได้เฉพาะ agent ที่รับข้อความจากเว็บ'))
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
  const boxEl = h('div', { class: 'advice policy' }, h('b', null, '⚙️ ลด model อัตโนมัติเมื่อ quota เกิน'))
  let p, ms
  try { p = (await api('/policy')).policy; ms = await loadModels() } catch (e) { return boxEl }
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
      toast(on.checked ? 'เปิดลด model อัตโนมัติที่ ' + th.value + '%' : 'ปิดลด model อัตโนมัติแล้ว (จะคืน model เดิมให้)')
    } catch (e) { toast('บันทึกไม่ได้: ' + e.message, true) }
  }
  boxEl.append(
    h('label', { class: 'polRow' }, on, h('span', null, 'เปิดใช้')),
    h('label', { class: 'polRow' }, h('span', null, 'เมื่อใช้ไปเกิน'), th, h('span', null, '%')),
    h('label', { class: 'polRow' }, h('span', null, 'Claude →'), cm),
    h('label', { class: 'polRow' }, h('span', null, 'Codex (session ใหม่) →'), xm),
    h('p', { class: 'note' }, 'ตรวจทุก 1 นาที · คืน model เดิมเมื่อใช้ลดลงต่ำกว่าเกณฑ์ 15 จุด หรือรอบรีเซ็ต · บันทึก audit ทุกครั้ง'),
    p.lastAction ? h('p', { class: 'note' }, 'ล่าสุด: ' + p.lastAction + ' · ' + ago(p.lastActionAt)) : '',
    h('button', { type: 'button', class: 'pri', onclick: save }, 'บันทึก'))
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
      toast(io.checked ? 'เปิดงานช่วงว่าง: ว่างเกิน ' + im.value + ' นาที และ quota ต่ำกว่า ' + iq.value + '%' : 'ปิดงานช่วงว่างแล้ว')
    } catch (e) { toast('บันทึกไม่ได้: ' + e.message, true) }
  }
  boxEl.append(h('hr'), h('b', null, '💡 ใช้ช่วงว่างคิดงาน (เสนอก่อน รออนุมัติ)'),
    h('label', { class: 'polRow' }, io, h('span', null, 'เปิดใช้')),
    h('label', { class: 'polRow' }, h('span', null, 'ว่างเกิน'), im, h('span', null, 'นาที')),
    h('label', { class: 'polRow' }, h('span', null, 'และ quota ต่ำกว่า'), iq, h('span', null, '%')),
    h('label', { class: 'polRow' }, h('span', null, 'ให้ agent ใน workspace'), iw),
    h('p', { class: 'note' }, 'agent จะคิดไอเดียเกม asset และจุดเสี่ยง/ช้า (ปรึกษา Codex แบบอ่านอย่างเดียวได้) แล้วส่งเป็นหน้าตรวจงานในห้องทดสอบ — ห้ามแก้โค้ดจนกว่าคุณอนุมัติ · สูงสุด 1 ครั้งต่อ 6 ชม. และไม่ส่งซ้ำถ้ายังมีข้อเสนอรอตรวจ'),
    p.lastIdeaAt ? h('p', { class: 'note' }, 'ส่งล่าสุด ' + ago(p.lastIdeaAt)) : '',
    h('button', { type: 'button', class: 'pri', onclick: saveIdle }, 'บันทึก'))
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
  for (const m of ask.matchAll(/\((?:เช่น|ตัวอย่าง|e\.g\.,?)\s*([^)]{2,80})\)/gi)) add('ใช่ ' + m[1])
  if (/ไหม|หรือไม่|ใช่ไหม|\?\s*$/.test(ask)) { add('ใช่'); add('ไม่') }
  if (!out.length && t) add('ทำต่อได้เลย')
  if (t) add('สรุปสถานะล่าสุดให้หน่อย')
  return out.slice(0, 4)
}
function drawSuggest () {
  const box = $('suggest')
  const list = composerSession() ? (st.suggest || []) : []
  box.hidden = !list.length
  box.replaceChildren(...list.map((v, i) => h('button', { type: 'button', class: 'sug' + (i === 0 ? ' first' : ''), title: i === 0 ? 'กด Tab เพื่อใช้' : 'คลิกเพื่อใช้', onclick: () => useSuggest(v) }, i === 0 ? h('kbd', null, 'Tab') : '', v)))
}
function useSuggest (v) {
  const ta = $('askText')
  ta.value = v
  ta.focus()
  ta.setSelectionRange(v.length, v.length)
  growText()
}

// ----- reviews: HTML pages agents file for the operator to check -----
function openReviewsOf (sid) {
  return (st.reviews || []).filter(r => r.status === 'open' && r.sessionId && sid.startsWith(r.sessionId.slice(0, 8)) && r.sessionId.startsWith(sid.slice(0, 8)))
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
      h('small', null, (sess ? sess.displayName : r.runtime === 'codexCli' ? 'Codex' : 'ไม่ระบุ session') + ' · ' + ago(r.createdAt)),
      r.summary ? h('p', null, r.summary) : null)
  }
  return h('div', { class: 'revList' },
    h('h3', null, 'งานรอตรวจ (' + open.length + ')' + (inPop ? '' : ' · ห้องทดสอบ')),
    open.length ? open.map(row) : h('p', { class: 'note' }, 'ยังไม่มีหน้าตรวจงาน — ให้ agent สร้างหน้า HTML สรุปงานแล้วรัน: aiagent review --title "…" page.html'),
    recent.length ? h('h3', null, 'ตรวจแล้วล่าสุด') : null, recent.map(row))
}

async function openReview (id) {
  const r = (st.reviews || []).find(x => x.id === id)
  if (!r) return
  const sess = sessOfReview(r)
  $('reviewTitle').textContent = '🧪 ตรวจงาน · ' + r.title
  $('reviewFrame').removeAttribute('src')
  try {
    const t = await api('/reviews/' + encodeURIComponent(id) + '/view', { method: 'POST' })
    $('reviewFrame').src = t.url
  } catch (e) { toast('เปิดหน้าตรวจไม่ได้: ' + e.message, true) }
  const note = h('textarea', { rows: 5, placeholder: 'หมายเหตุ / สิ่งที่ต้องแก้ (ถ้าขอแก้ต้องใส่)' })
  const out = h('pre', { class: 'revReply', hidden: true })
  const canSend = sess && sess.live && sess.messageVia && r.status === 'open'
  const decide = async (decision, send) => {
    if (decision === 'revise' && !note.value.trim()) { note.focus(); toast('บอกสิ่งที่ต้องแก้ก่อน', true); return }
    try {
      const res = await api('/reviews/' + encodeURIComponent(id) + '/decision', { method: 'POST', body: { decision, note: note.value, send } })
      out.hidden = false
      out.textContent = res.reply
      if (send) for (const b of $('reviewSide').querySelectorAll('button.pri, button.warn')) b.disabled = true
      if (!send) copyText(res.reply, { textContent: '' })
      toast(send ? 'ส่งให้ ' + sess.displayName + ' แล้ว (' + res.via + ')' : 'คัดลอกข้อความตอบกลับแล้ว — วางใน session ได้เลย')
      st.revAt = 0
      poll()
    } catch (e) { toast('ไม่สำเร็จ: ' + e.message, true) }
  }
  $('reviewSide').replaceChildren(...[
    h('dl', { class: 'kv' },
      h('dt', null, 'จาก'), h('dd', null, sess ? sess.displayName : (r.runtime === 'codexCli' ? 'Codex' : 'ไม่ระบุ session')),
      h('dt', null, 'ส่งเมื่อ'), h('dd', null, fmtTime(r.createdAt) + ' · ' + ago(r.createdAt)),
      h('dt', null, 'สถานะ'), h('dd', null, r.status === 'open' ? 'รอตรวจ' : r.status === 'approved' ? 'ตกลงแล้ว' : 'ขอแก้แล้ว'),
      r.cwd ? h('dt', null, 'โฟลเดอร์') : null, r.cwd ? h('dd', { class: 'mono' }, r.cwd) : null),
    r.summary ? h('p', null, r.summary) : null,
    note,
    h('div', { class: 'revBtns' },
      h('b', null, '✅ ตกลง ปิดงาน'),
      h('button', { type: 'button', class: 'ghost', onclick: () => decide('approve', false) }, 'คัดลอกข้อความ'),
      h('button', { type: 'button', class: 'pri', disabled: !canSend, onclick: () => decide('approve', true) }, 'ส่งให้ agent')),
    h('div', { class: 'revBtns' },
      h('b', null, '✏️ ขอแก้ไขเพิ่มเติม'),
      h('button', { type: 'button', class: 'ghost', onclick: () => decide('revise', false) }, 'คัดลอกข้อความ'),
      h('button', { type: 'button', class: 'warn', disabled: !canSend, onclick: () => decide('revise', true) }, 'ส่งให้ agent')),
    canSend ? null : h('p', { class: 'note' }, r.status !== 'open' ? 'งานนี้ตรวจไปแล้ว — ส่งซ้ำไม่ได้ (คัดลอกข้อความได้)' : 'ส่งตรงไม่ได้ (session ไม่ได้รันอยู่หรือรับข้อความจากเว็บไม่ได้) — กดคัดลอกแล้ววางใน Claude / Codex เอง'),
    out].filter(Boolean))
  $('reviewDlg').showModal()
}

// ----- accounts: switch the Claude / Codex sign-in on this box -----
const accountName = { claude: 'Claude Code', codex: 'Codex' }
async function openAccounts () {
  $('accountsDlg').showModal()
  await drawAccounts()
}
async function drawAccounts () {
  clearTimeout(st.accTimer)
  if (!$('accountsDlg').open) return
  let accs
  try { accs = (await api('/accounts')).accounts } catch (e) { $('accountsBody').replaceChildren(h('p', { class: 'note' }, 'อ่านสถานะบัญชีไม่ได้: ' + e.message)); return }
  $('accountsBody').replaceChildren(...accs.map(accountCard).filter(Boolean), h('p', { class: 'note' }, 'สลับบัญชี = เปิดหน้า login ของผู้ให้บริการ (ใช้ขั้นตอนของ CLI เอง ไม่ผ่าน shell) · ทุกขั้นบันทึก audit'))
  if (accs.some(a => a.login && a.login.running)) st.accTimer = setTimeout(drawAccounts, 2500)
}
function accountCard (a) {
  const p = a.provider
  const f = a.login
  const email = h('input', { type: 'email', placeholder: 'อีเมลบัญชีที่จะใช้ (ไม่บังคับ)', autocomplete: 'off' })
  const code = h('input', { type: 'text', placeholder: 'วางโค้ดที่ได้หลัง login', autocomplete: 'off', spellcheck: 'false' })
  const call = async (path, body, msg) => {
    try { await api('/accounts/' + p + '/' + path, { method: 'POST', body: body || {} }); if (msg) toast(msg) } catch (e) { toast('ไม่สำเร็จ: ' + e.message, true) }
    drawAccounts()
  }
  const start = async () => {
    const warn = p === 'codex'
      ? 'Codex จะ logout ทันทีที่เริ่ม (เป็นพฤติกรรมของ codex เอง) — agent Codex ทุกตัวบนเครื่องจะใช้ไม่ได้จนกว่า login ใหม่เสร็จ'
      : 'บัญชีเดิมยังใช้ได้จนกว่าจะวางโค้ดสำเร็จ — หลังสลับ session ที่เปิดอยู่จะใช้บัญชีใหม่เมื่อรีเฟรช token'
    if (!await confirmBox('สลับบัญชี ' + accountName[p], warn, a.email ? 'ปัจจุบัน: ' + a.email : (a.detail || ''), 'เริ่ม login')) return
    call('login', { email: email.value.trim() })
  }
  const logout = async () => {
    if (!await confirmBox('Logout ' + accountName[p], 'agent ' + accountName[p] + ' ทุกตัวบนเครื่องนี้จะหยุดใช้งานได้จนกว่าจะ login ใหม่', a.email || a.method || '', 'Logout')) return
    call('logout', null, 'logout ' + accountName[p] + ' แล้ว')
  }
  return h('div', { class: 'accCard' },
    h('div', { class: 'accTop' }, h('b', null, accountName[p]), h('span', { class: 'pill ' + (a.loggedIn ? 'normal' : 'reserve') }, h('i'), a.loggedIn ? 'login อยู่' : 'ยังไม่ได้ login')),
    a.loggedIn ? h('p', null, [a.email, a.method, a.org].filter(Boolean).join(' · ') + (a.expiresAt ? ' · หมดอายุ ' + new Date(a.expiresAt).toLocaleString('th-TH', { dateStyle: 'short', timeStyle: 'short' }) : '')) : (a.detail ? h('p', { class: 'note' }, a.detail) : null),
    f && f.running
      ? h('div', { class: 'accFlow' },
        h('p', null, '1) เปิดลิงก์นี้แล้ว sign in ด้วยบัญชีที่ต้องการ'),
        f.url ? h('a', { href: f.url, target: '_blank', rel: 'noopener noreferrer', class: 'pri accLink' }, 'เปิดหน้า login ↗') : h('p', { class: 'note' }, 'กำลังรอลิงก์…'),
        f.code ? h('p', null, '2) กรอกรหัสนี้ในหน้านั้น: ', h('code', { class: 'devCode' }, f.code), ' ', h('button', { type: 'button', class: 'ghost', onclick: e => copyText(f.code, e.currentTarget) }, 'คัดลอก')) : null,
        p === 'claude' ? h('div', { class: 'accRow' }, h('span', null, '2) วางโค้ดที่หน้าเว็บแสดง:'), code, h('button', { type: 'button', class: 'pri', onclick: () => call('code', { code: code.value }, 'ส่งโค้ดแล้ว กำลังตรวจ…') }, 'ยืนยันโค้ด')) : h('p', { class: 'note' }, 'Codex จะเสร็จเองเมื่อกรอกรหัสบนเว็บแล้ว'),
        f.output ? h('pre', { class: 'accOut' }, f.output) : null,
        h('button', { type: 'button', class: 'ghost', onclick: () => call('cancel', null, 'ยกเลิกแล้ว') }, 'ยกเลิก login'))
      : h('div', { class: 'accRow' },
        p === 'claude' ? email : null,
        h('button', { type: 'button', class: 'pri', onclick: start }, a.loggedIn ? 'สลับบัญชี' : 'Login'),
        a.loggedIn ? h('button', { type: 'button', class: 'warn', onclick: logout }, 'Logout') : null),
    f && !f.running && f.output ? h('pre', { class: 'accOut' }, f.output) : null)
}
$('accountsBtn').addEventListener('click', openAccounts)

function avatarSvg (fill) {
  const v = s('svg', { width: 30, height: 30, viewBox: '0 0 34 34', 'aria-hidden': 'true' })
  s('circle', { cx: 17, cy: 11, r: 7, fill }, v)
  s('rect', { x: 7, y: 19, width: 20, height: 13, rx: 6, fill, opacity: 0.75 }, v)
  return v
}

// ---------- actions ----------
function confirmBox (title, body, text, okLabel) {
  return new Promise(resolve => {
    const d = $('confirm')
    $('confirmTitle').textContent = title
    $('confirmBody').textContent = body
    $('confirmText').textContent = text || ''
    $('confirmText').hidden = !text
    $('confirmOk').textContent = okLabel || 'ยืนยัน'
    d.returnValue = ''
    d.addEventListener('close', () => resolve(d.returnValue === 'ok'), { once: true })
    d.showModal()
  })
}

function toast (msg, bad) {
  const b = $('banner')
  b.hidden = false
  b.className = bad ? 'banner bad' : 'banner'
  b.textContent = msg
}

async function act (path, body, okMsg) {
  try {
    const res = await api(path, { method: 'POST', body })
    toast(okMsg(res || {}))
    poll()
    return true
  } catch (e) {
    if (e.message !== 'auth') toast('ส่งไม่สำเร็จ: ' + e.message, true)
    return false
  }
}

function focusComposer (ws, mode) {
  st.target = ws
  st.blockReason = ''
  $('askWs').value = ws
  updateComposerHint()
  $('askMode').value = mode
  $('askText').focus()
}

$('askWs').addEventListener('change', () => { st.target = $('askWs').value; st.blockReason = ''; updateComposerHint() })
$('ask').addEventListener('submit', async e => {
  e.preventDefault()
  const sess = composerSession()
  const ta = $('askText')
  if (sess) return sendChat(sess, ta, $('askThumbs'))
  const ws = $('askWs').value
  const mode = $('askMode').value
  let text = expandPastes(ta.value).trim()
  const files = st.attach || []
  if (!ws || (!text && !files.length)) return
  const w = st.snap && st.snap.workspaces.find(x => x.name === ws)
  const where = ws + (w ? ' (tmux ' + w.tmuxSession + ', ' + w.agentCommand + ')' : '')
  const ok = await confirmBox(
    mode === 'mission' ? 'สร้างงานใหม่' : 'ส่งข้อความถึง agent',
    'ข้อความนี้จะถูกพิมพ์เข้า agent ใน ' + where + (mode === 'mission' ? ' และติดตามเป็นงาน kcmd eng' : ''),
    text + (files.length ? '\n\n+ รูปแนบ ' + files.length + ' รูป' : ''),
    'ส่งเลย'
  )
  if (!ok) return
  if (files.length) {
    try {
      const paths = []
      for (const f of files) paths.push(await uploadImage(f.blob))
      text = (text || 'ดูรูปที่แนบ') + '\n\n' + paths.map((p, i) => 'รูปแนบ ' + (i + 1) + ': ' + p).join('\n')
    } catch (err) {
      toast('อัปโหลดรูปไม่สำเร็จ: ' + err.message, true)
      return
    }
  }
  const sent = mode === 'mission'
    ? await act('/missions', { workspace: ws, task: text }, r => 'สร้างงาน ' + (r.runId || '') + ' ใน ' + ws + ' แล้ว')
    : await act('/messages', { workspace: ws, message: text }, () => 'ส่งข้อความถึง ' + ws + ' แล้ว')
  if (sent) {
    ta.value = ''
    clearPastes()
    for (const f of files) URL.revokeObjectURL(f.url)
    st.attach = []
    drawThumbs($('askThumbs'))
    growText()
  }
})

function addFiles (list) {
  for (const f of list) {
    if (f && /^image\/(png|jpeg|gif|webp)$/.test(f.type) && (st.attach || []).length < 10) (st.attach = st.attach || []).push({ blob: f, url: URL.createObjectURL(f) })
  }
  drawThumbs($('askThumbs'))
}
function growText () {
  const ta = $('askText')
  const keep = ta.scrollTop
  const atEnd = ta.selectionEnd >= ta.value.length - 1
  ta.style.height = 'auto'
  ta.style.height = Math.min(240, ta.scrollHeight + 2) + 'px'
  ta.scrollTop = atEnd ? ta.scrollHeight : keep
  const multi = ta.value.includes('\n') || ta.scrollHeight > 52
  ta.classList.toggle('multi', multi)
  const n = ta.value ? ta.value.split('\n').length : 0
  if (multi) ta.parentElement.dataset.lines = (n > 1 ? n + ' บรรทัด' : 'ยาว') + ' ↕'
  else delete ta.parentElement.dataset.lines
}
$('askText').addEventListener('focus', growText)
$('askText').addEventListener('input', growText)
$('askText').addEventListener('keydown', e => {
  if (e.key === 'Tab' && !e.shiftKey && (st.suggest || []).length && composerSession()) {
    const v = st.suggest[0]
    const cur = $('askText').value
    if (!cur.trim() || v.startsWith(cur)) { e.preventDefault(); useSuggest(v); return }
  }
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); $('ask').requestSubmit() }
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

function openLightbox (src) {
  $('lightboxImg').src = src
  $('lightboxImg').hidden = false
  $('lightbox').showModal()
}
$('lightbox').addEventListener('click', e => { if (e.target === $('lightbox') || e.target === $('lightboxImg')) $('lightbox').close() })

async function doAnswer (run, answer) {
  answer = (answer || '').trim()
  if (!answer) return
  if (!await confirmBox('ตอบ ' + run.id, 'คำตอบจะถูกพิมพ์เข้า agent ใน ' + run.tmuxSession, answer, 'ส่งคำตอบ')) return
  act('/runs/' + encodeURIComponent(run.id) + '/answer', { answer }, () => 'ส่งคำตอบให้ ' + run.id + ' แล้ว')
}

async function doStop (sess) {
  const short = sess.sessionId.slice(0, 8)
  const how = sess.stopVia === 'claudeStop'
    ? 'จะรัน claude stop ' + short + ' — หยุด background session นี้ บทสนทนายังเก็บไว้ เปิดต่อได้ด้วย claude attach ' + short
    : 'จะรัน kcmd restart ' + sess.dispatchVia + ' — ปิดโปรแกรม agent ในหน้าต่าง tmux ของ workspace นี้ทันที งานที่ทำค้างจะหยุด'
  const self = sess.dispatchVia ? '\nหมายเหตุ: agent นี้คือตัวที่รับงานจากเว็บผ่าน ' + sess.dispatchVia + ' หยุดแล้วจะสั่งงานจากเว็บไม่ได้จนกว่าจะเปิดใหม่' : ''
  if (!await confirmBox('หยุด ' + sess.displayName + '?', how + self, sess.title || '', 'หยุด agent')) return
  act('/sessions/' + encodeURIComponent(sess.sessionId) + '/stop', null, r => 'หยุด ' + sess.displayName + ' แล้ว (' + (r.via || '') + ')')
}

async function doRunStatus (run, action) {
  const label = action === 'approve' ? 'อนุมัติ' : 'ยกเลิก'
  if (!await confirmBox(label + ' ' + run.id, 'เปลี่ยนสถานะงานใน kcmd เป็น ' + (action === 'approve' ? 'READY' : 'ABORTED') + ' (ไม่ปิด process ของ agent)', run.task.slice(0, 300), label)) return
  act('/runs/' + encodeURIComponent(run.id) + '/' + action, null, () => label + ' ' + run.id + ' แล้ว')
}

// ---------- login / views ----------
function showLogin (msg) {
  clearTimeout(st.timer)
  const d = $('login')
  $('loginErr').textContent = msg || ''
  if (!d.open) d.showModal()
  $('loginToken').focus()
}
$('loginForm').addEventListener('submit', e => {
  e.preventDefault()
  const t = $('loginToken').value.trim()
  if (!t) return
  st.token = t
  safeSet(tokenKey, t)
  $('loginToken').value = ''
  $('login').close()
  poll()
})
$('login').addEventListener('cancel', e => e.preventDefault())
$('logout').addEventListener('click', () => {
  st.token = ''
  safeSet(tokenKey, '')
  st.snap = null
  showLogin()
})
function setView (view) {
  st.view = view
  $('viewMap').classList.toggle('on', view === 'map')
  $('viewList').classList.toggle('on', view === 'list')
  $('viewMap').setAttribute('aria-pressed', String(view === 'map'))
  $('viewList').setAttribute('aria-pressed', String(view === 'list'))
  $('village').classList.toggle('off', view !== 'map')
  $('sceneTools').hidden = view !== 'map'
  $('list').hidden = view !== 'list'
  if (st.snap) render()
}
$('viewMap').addEventListener('click', () => setView('map'))
$('stageBack').addEventListener('click', () => goBack())
// ----- follow the selected agent from room to room -----
function followActor () {
  const id = st.follow
  if (!id) return null
  const sess = st.snap.sessions.find(x => x.sessionId === id.split(':')[0])
  if (!sess || !sess.live) return null
  const house = originOf(sess)
  const a = actorsFor(house).find(x => x.id === id)
  return a ? Object.assign({ house }, a) : null
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
  chip.textContent = '📍 ติดตาม ' + (a.name.length > 22 ? a.name.slice(0, 21) + '…' : a.name) + ' · ' + (roomTitle[a.room] || '') + '  ✕'
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
$('zoomIn').addEventListener('click', () => { st.zoom = Math.min(2, (st.zoom || 1) + 0.2); applyZoom() })
$('zoomOut').addEventListener('click', () => { st.zoom = Math.max(0.6, (st.zoom || 1) - 0.2); applyZoom() })
$('fullBtn').addEventListener('click', () => {
  const el = $('stage')
  try {
    if (document.fullscreenElement) document.exitFullscreen()
    else el.requestFullscreen()
  } catch (e) { /* fullscreen not allowed */ }
})
window.addEventListener('resize', () => applyZoom())
try { new ResizeObserver(() => applyZoom()).observe($('village')) } catch (e) { /* older browser: window resize only */ }
function setPanel (open) {
  st.panelOpen = open
  document.querySelector('.main').classList.toggle('collapsed', !open)
  $('togglePanel').textContent = open ? 'ซ่อนแผงขวา ⟩' : '⟨ แสดงแผง'
  $('togglePanel').setAttribute('aria-expanded', String(open))
  try { localStorage.setItem('aiagentPanel', open ? '1' : '0') } catch (e) { /* per-tab only */ }
  setTimeout(applyZoom, 60)
}
$('togglePanel').addEventListener('click', () => setPanel(!st.panelOpen))
let panelPref = '1'
try { panelPref = localStorage.getItem('aiagentPanel') || '1' } catch (e) { panelPref = '1' }
setPanel(panelPref === '1')
document.addEventListener('fullscreenchange', () => setTimeout(applyZoom, 50))
$('viewList').addEventListener('click', () => setView('list'))
document.addEventListener('keydown', e => {
  if (e.key !== 'Escape' || document.querySelector('dialog[open]')) return
  if (st.sel) back()
  else if (st.house) goBack()
})
setInterval(renderConn, 2000)
if (window.matchMedia('(max-width: 900px)').matches) setView('list')
poll()
