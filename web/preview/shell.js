// The shell of a room's page. It runs on an origin shared with other
// applications and frames content it does not trust: the frame is sandboxed
// without same-origin, and what it posts here is only ever compared.
'use strict'
;(function () {
  const $ = id => document.getElementById(id)
  const room = location.pathname.split('/')[2]
  const api = '/dev/' + room + '/-/'
  const hash = new URLSearchParams(location.hash.slice(1))
  history.replaceState(null, '', location.pathname)
  let lang = 'th'
  try { lang = hash.get('l') || sessionStorage.getItem('pvLang') || 'th'; sessionStorage.setItem('pvLang', lang) } catch (e) { /* fine without */ }
  const EN = {
    'โหลดใหม่': 'Reload', 'เต็มจอ': 'Full', 'จับภาพ': 'Capture', 'กำลังจับภาพ…': 'Capturing…', 'ส่งเข้ารีวิว': 'Send to review',
    'กำลังเปิด…': 'Opening…', 'เปิดหน้านี้จากปุ่มพรีวิวในเว็บ aiagent': 'Open this page with the preview button in the aiagent web',
    'ลิงก์นี้ใช้ไปแล้วหรือหมดอายุ เปิดใหม่จากปุ่มพรีวิวในเว็บ aiagent': 'This link was used or has expired. Open it again from the aiagent web',
    'ยังไม่มีอะไรให้แสดง ให้ agent สร้างหน้าใน work/<โปรเจกต์>/site หรือเขียน work/preview.json': 'Nothing to show yet. Ask the agent to build a page in work/<project>/site or to write work/preview.json',
    'ห้องนี้ยังไม่ตอบ ลองใหม่อีกครั้ง': 'This room does not answer yet, trying again', 'การจับภาพต้องติดตั้ง browser บนเครื่องก่อน': 'Capture needs a browser installed on the machine',
    'จับภาพไม่สำเร็จ: ': 'Capture failed: ', 'ส่งแล้ว ดูได้ในแท็บรีวิวของ aiagent': 'Sent. It is in the Review tab of aiagent', 'ส่งไม่สำเร็จ: ': 'Not sent: ',
    'ยังไม่มี dev server ของห้องนี้ที่พอร์ตนี้': 'No dev server of this room on that port yet',
    'หมายเหตุถึงผู้ตรวจ (ไม่บังคับ)': 'Note for the reviewer (optional)', 'ปิด': 'Close', 'ความกว้าง': 'Width'
  }
  const tr = s => (lang === 'en' && EN[s]) || s
  document.documentElement.lang = lang
  document.title = room + ' · preview'
  $('room').textContent = room
  $('reload').textContent = tr('โหลดใหม่')
  $('capture').textContent = tr('จับภาพ')
  $('send').textContent = tr('ส่งเข้ารีวิว')
  $('note').placeholder = tr('หมายเหตุถึงผู้ตรวจ (ไม่บังคับ)')
  $('note').setAttribute('aria-label', tr('หมายเหตุถึงผู้ตรวจ (ไม่บังคับ)'))
  $('close').setAttribute('aria-label', tr('ปิด'))
  $('widths').setAttribute('aria-label', tr('ความกว้าง'))
  document.querySelector('[data-w=full]').textContent = tr('เต็มจอ')
  $('noCapture').textContent = tr('การจับภาพต้องติดตั้ง browser บนเครื่องก่อน')

  async function call (path, body) {
    const res = await fetch(api + path, {
      method: body ? 'POST' : 'GET', cache: 'no-store', credentials: 'same-origin',
      headers: Object.assign({ 'X-Aiagent-Preview': '1' }, body ? { 'Content-Type': 'application/json' } : {}),
      body: body ? JSON.stringify(body) : undefined
    })
    let b = null
    try { b = await res.json() } catch (e) { /* not JSON */ }
    if (!res.ok) { const e = new Error((b && b.message) || 'HTTP ' + res.status); e.status = res.status; throw e }
    return b.details
  }
  function say (text) { $('msg').textContent = text }
  function show (on) { $('stage').hidden = !on; $('tools').hidden = !on }

  let base = '' // where the content is; changes when the capability does
  let version = ''
  let frameReady = false // the page in the frame can reload itself in place
  function load () { frameReady = false; $('frame').src = base }
  function reload () {
    if (frameReady) { frameReady = false; $('frame').contentWindow.postMessage('aiagentPreviewReload', '*') } else load()
  }
  addEventListener('message', e => {
    if (e.source === $('frame').contentWindow && e.data === 'aiagentPreviewReady') frameReady = true
  })

  let timer = 0
  async function poll () {
    clearTimeout(timer)
    let s
    try { s = await call('state') } catch (e) {
      if (e.status === 401 || e.status === 403) {
        show(false); $('frame').removeAttribute('src'); base = ''
        say(tr('เปิดหน้านี้จากปุ่มพรีวิวในเว็บ aiagent'))
        return
      }
      timer = setTimeout(poll, 4000)
      return
    }
    const src = s.source
    const ready = src.kind === 'static' || src.kind === 'port'
    $('src').textContent = src.kind === 'port' ? 'port ' + src.port : (src.folder || '')
    $('capture').hidden = !s.capture
    $('noCapture').hidden = s.capture
    show(ready)
    if (!ready) say(src.error === 'down' ? tr('ห้องนี้ยังไม่ตอบ ลองใหม่อีกครั้ง') : (src.error || tr('ยังไม่มีอะไรให้แสดง ให้ agent สร้างหน้าใน work/<โปรเจกต์>/site หรือเขียน work/preview.json')))
    else {
      say(src.version === 'down' ? tr('ยังไม่มี dev server ของห้องนี้ที่พอร์ตนี้') : '')
      const v = src.kind + src.port + src.version
      if (s.base !== base) { base = s.base; load() } else if (v !== version) reload()
      version = v
    }
    timer = setTimeout(poll, document.hidden ? 30000 : 1500)
  }
  document.addEventListener('visibilitychange', () => { if (!document.hidden && base) poll() })

  $('reload').addEventListener('click', load)
  $('widths').addEventListener('click', e => {
    const w = e.target.dataset && e.target.dataset.w
    if (!w) return
    $('frame').className = w === 'full' ? '' : 'w' + w
    for (const b of $('widths').children) b.setAttribute('aria-pressed', String(b === e.target))
  })
  let shot = ''
  $('capture').addEventListener('click', async () => {
    const b = $('capture')
    b.disabled = true; b.textContent = tr('กำลังจับภาพ…')
    try {
      const r = await call('capture', {})
      shot = r.id
      $('pics').replaceChildren(...r.images.map(i => {
        const img = document.createElement('img')
        img.alt = i.width + ' px'
        img.src = 'data:image/jpeg;base64,' + i.data
        return img
      }))
      $('sent').textContent = ''
      $('send').disabled = false
      $('shots').showModal()
      say('')
    } catch (e) { say(tr('จับภาพไม่สำเร็จ: ') + e.message) }
    b.disabled = false; b.textContent = tr('จับภาพ')
  })
  $('send').addEventListener('click', async () => {
    $('send').disabled = true
    try {
      await call('review', { id: shot, note: $('note').value })
      $('note').value = ''
      $('sent').textContent = tr('ส่งแล้ว ดูได้ในแท็บรีวิวของ aiagent')
    } catch (e) { $('sent').textContent = tr('ส่งไม่สำเร็จ: ') + e.message; $('send').disabled = false }
  })

  async function start () {
    say(tr('กำลังเปิด…'))
    const ticket = hash.get('t')
    if (ticket) {
      try { await call('enter', { ticket }) } catch (e) {
        say(tr('ลิงก์นี้ใช้ไปแล้วหรือหมดอายุ เปิดใหม่จากปุ่มพรีวิวในเว็บ aiagent'))
        return
      }
    }
    say('')
    poll()
  }
  addEventListener('hashchange', () => location.reload())
  start()
})()
