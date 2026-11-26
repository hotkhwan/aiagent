// Screenshots for the neutral guide: the real page, with every API answer
// replaced by made-up data, so no real name, host or conversation is shown.
import { chromium } from 'playwright'
import { readFileSync } from 'fs'
// needs a running `aiagent serve --listen 127.0.0.1:8999` (any state dir, enabled)
//   ROLE=admin|user   which set of pictures
//   OUT=dir/          where they go (default ./img/)
//   NAMES=a,b         the two made-up rooms (default alice,bob)
//   TOKEN=file        optional: that service's token; review pages are then read
//                     from the service itself (file a made-up page with `aiagent review` first)
//   REVIEW=title      which filed review page to open (default: the newest)
//   DEMO=file.html    optional: the made-up page shown in the preview pictures
const OUT=process.env.OUT||new URL('./img/', import.meta.url).pathname, role=process.env.ROLE||'admin'
const [A,B]=(process.env.NAMES||'alice,bob').split(','), TOKEN=process.env.TOKEN?readFileSync(process.env.TOKEN,'utf8').trim():''
const BASE='http://127.0.0.1:8999', SHELL=new URL('../../web/preview/', import.meta.url).pathname
const now=Date.now(), iso=m=>new Date(now-m*60000).toISOString()
const S=(id,name,o={})=>Object.assign({sessionId:id,runtimeType:'claudeCode',providerId:'anthropic',displayName:name,owner:name,cwd:'',repoId:'project',status:'idle',live:true,liveEvidence:'process',activityType:'idle',startedAt:iso(180),lastEventAt:iso(4),usage:{inputTokens:39000,outputTokens:5000,source:'actual'},kind:'bg',messageVia:'agcmd',dispatchVia:name,stopVia:'agcmdRestart',model:'claude-opus-5-5',gitBranch:'feat/login'},o)
const SA='22222222-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
const sessions= role==='admin' ? [
 S('11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa','main',{protected:true,status:'running',activityType:'planning',messageVia:'relay',dispatchVia:'',look:'cx-kid6',lastEventAt:iso(0)}),
 S(SA,A,{look:'dev',status:'running',activityType:'coding',lastEventAt:iso(1)}),
 S('33333333-aaaa-4aaa-8aaa-aaaaaaaaaaaa',B,{look:'cx-fox',status:'needsInput',lastEventAt:iso(6)}),
 S('codex-proc-1','Codex',{runtimeType:'codexCli',providerId:'openai',look:'cx-bot3',dispatchVia:'codex',kind:'interactive',model:''}),
 S('agy-agy','Antigravity',{runtimeType:'agyCli',providerId:'google',look:'cx-kid1',dispatchVia:'agy',kind:'interactive',model:''}),
] : [
 S(SA,A,{look:'dev',status:'running',activityType:'coding',lastEventAt:iso(1)}),
 S('33333333-aaaa-4aaa-8aaa-aaaaaaaaaaaa',B,{look:'cx-fox',readOnly:true,messageVia:'',dispatchVia:'',stopVia:'',messageBlocked:'ห้องของ '+B+' — อ่านบทสนทนาได้ ส่งข้อความไม่ได้',stopBlocked:'x',lastEventAt:iso(6)}),
 {sessionId:'11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa',runtimeType:'claudeCode',providerId:'anthropic',displayName:'admin',status:'running',live:true,liveEvidence:'process',activityType:'unknown',startedAt:iso(200),lastEventAt:iso(0),kind:'bg',protected:true,look:'cx-kid6',messageBlocked:'session ของผู้ดูแล',stopBlocked:'x',usage:{}},
]
const q=(p,w,u,r)=>({providerId:p,mode:u>80?'critical':'normal',confidence:'actual',usedPercent:u,remainingPercent:100-u,windowMinutes:w,resetsAt:new Date(now+r*3600000).toISOString(),observedAt:iso(2),source:'plan'})
const msgs=[
 {role:'user',at:iso(22),text:'เพิ่มปุ่ม "ลืมรหัสผ่าน" ในหน้า login\n\nเสร็จเมื่อ: กดแล้วไปหน้า /reset และ test ผ่าน\nห้ามแตะ: ไฟล์ config'},
 {role:'assistant',at:iso(21),text:'รับทราบครับ แผนคือ\n1. เพิ่มปุ่มใน LoginForm\n2. เพิ่ม route /reset\n3. เขียน test 2 ข้อ\n\nเริ่มทำเลยครับ'},
 {role:'assistant',at:iso(6),text:'เสร็จแล้วครับ\n- แก้ 3 ไฟล์ใน src/login\n- test ผ่าน 14/14\n- commit a1b2c3d บน branch feat/login\n\nรอคุณตรวจที่หน้ารีวิว'},
 {role:'user',at:iso(4),text:'ดีครับ ขอภาพหน้าจอก่อนและหลังด้วย'},
]
// a question dialog of Claude Code (AskUserQuestion) as the service reads it off the screen
const ASK={question:'',options:[{n:1,label:'การ์ด'},{n:2,label:'ตาราง'},{n:3,label:'Type something'}],ask:{tabs:[{header:'รูปแบบ'},{header:'ตัวกรอง'}],active:0,hasSubmit:true,chat:true,textOption:3,
 questions:[{header:'รูปแบบ',question:'บนมือถือ รายการควรแสดงแบบไหน',options:[{label:'การ์ด',detail:'หนึ่งรายการต่อการ์ด อ่านง่ายบนจอแคบ'},{label:'ตาราง',detail:'เหมือนจอกว้าง เลื่อนซ้ายขวาได้'}],freeText:true},
  {header:'ตัวกรอง',question:'ต้องการตัวกรองอะไรบ้าง',multiSelect:true,options:[{label:'ประเภท'},{label:'วันที่'},{label:'สถานะ'}],freeText:true}]}}
const ASK_SUBMIT={question:'',options:[{n:1,label:'Submit answers'},{n:2,label:'Cancel'}],ask:{tabs:[{header:'รูปแบบ',answered:true},{header:'ตัวกรอง',answered:true}],active:2,hasSubmit:true,review:true,summary:[{question:'บนมือถือ รายการควรแสดงแบบไหน',answer:'การ์ด'},{question:'ต้องการตัวกรองอะไรบ้าง',answer:'ประเภท, วันที่'}]}}
const VAULT=[{id:'v1',name:'gitlab-my-app',provider:'gitlab',purpose:'push my-app และเปิด merge request',createdAt:iso(3000),lastUsed:iso(40)},{id:'v2',name:'github-docs',provider:'github',purpose:'อ่าน repo เอกสาร',createdAt:iso(900)}]
const DEMO=process.env.DEMO?readFileSync(process.env.DEMO,'utf8'):'<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><style>body{margin:0;font:16px/1.5 system-ui,sans-serif;color:#1d2433;background:#f6f7fb}header{background:#2e4a9e;color:#fff;padding:18px 24px;font-weight:700;font-size:20px}main{padding:20px 24px;display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:16px}.c{background:#fff;border:1px solid #d9deea;border-radius:12px;padding:16px}.c b{display:block;font-size:17px}.c span{color:#5b6478;font-size:14px}.t{display:inline-block;margin-top:8px;padding:2px 10px;border-radius:99px;background:#e6f4ea;color:#1b6b43;font-size:13px}</style><header>งานของฉัน</header><main><div class=c><b>เพิ่มปุ่มลืมรหัสผ่าน</b><span>หน้า login · วันนี้ 10:20</span><br><i class=t>เสร็จแล้ว</i></div><div class=c><b>แก้ตารางล้นจอบนมือถือ</b><span>หน้ารายการ · วันนี้ 09:05</span><br><i class=t>เสร็จแล้ว</i></div><div class=c><b>เพิ่มตัวกรองตามวันที่</b><span>หน้ารายการ · เมื่อวาน</span><br><i class=t style="background:#fff1d6;color:#8a4b00">กำลังทำ</i></div></main>'
const b=await chromium.launch()
// what the next send to a session answers: 'hold' never answers, 'dup' is the
// service's duplicate refusal, 'lost' is a send that no transcript confirmed
let post='', ask=null
async function page(vp,lang='th'){const c=await b.newContext({viewport:vp,deviceScaleFactor:vp.width<500?3:2,locale:lang==='th'?'th-TH':'en-US',timezoneId:process.env.TZID||'Asia/Bangkok'}),p=await c.newPage()
 await p.route('**/aiagent/api/**',async r=>{const u=new URL(r.request().url()),path=u.pathname.replace('/aiagent/api',''),m=r.request().method();const J=d=>r.fulfill({contentType:'application/json',body:JSON.stringify({code:'SUCCESS',details:d})})
  if(TOKEN&&path.startsWith('/reviews'))return r.continue()
  if(m==='POST'&&/^\/sessions\/[^/]+\/message$/.test(path)){
   if(post==='hold')return
   if(post==='dup'&&!JSON.parse(r.request().postData()||'{}').resend)return r.fulfill({status:409,contentType:'application/json',body:JSON.stringify({code:'AIAGENT_DUPLICATE_SEND',message:'ข้อความเดียวกันนี้เพิ่งส่งถึงปลายทางเดียวกันเมื่อไม่กี่วินาทีก่อน จึงไม่ได้ส่งซ้ำ ถ้าตั้งใจส่งอีกครั้งให้กด "ส่งซ้ำ"'})})
   if(post==='lost')return J({via:'agcmd',delivery:'unknown'})}
  if(m==='POST'&&path==='/vault')return r.fulfill({status:400,contentType:'application/json',body:JSON.stringify({code:'AIAGENT_BAD_REQUEST',message:'ค่า token ต้องยาว 16-400 ตัว ไม่มีเว้นวรรคหรือเครื่องหมายคำพูด'})})
  if(m!=='GET')return r.abort()
  if(path==='/me')return J({role, name:role==='user'?A:undefined, osUser:role==='user'?A:undefined, preview:true})
  if(path==='/snapshot'){const d={generatedAt:new Date().toISOString(),version:'0.27.1'};d.host='my-server';d.sessions=sessions.map(s=>s.sessionId===SA&&ask?Object.assign({},s,{status:'needsInput',activityType:'waiting',prompt:ask}):s);d.runs=[];d.warnings=[];d.pending=[];d.workspaces=role==='admin'?[{name:'codex',tmuxSession:'site:codex',dir:'/srv/ai-work/codex',dirExists:true,tmuxAlive:true,agentCommand:'codex',agentReady:true},{name:'agy',tmuxSession:'site:agy',dir:'/srv/ai-work/agy',dirExists:true,tmuxAlive:true,agentCommand:'agy',agentReady:true}]:[{name:A,tmuxSession:'core:'+A,dir:'/home/'+A+'/work',dirExists:true,tmuxAlive:true,agentCommand:'claude',agentReady:true}]
   d.quotas=[q('anthropic',300,24,3),q('anthropic',10080,12,96),q('openai',300,9,2),q('openai',10080,30,50),q('google',300,2,4),q('google',10080,1,160)];d.summary={running:role==='admin'?2:2,needsInput:role==='admin'?1:0,completed:0,idle:role==='admin'?2:1,waitingApproval:0,lost:0,endedToday:0,runsDoneToday:0,runsAbortedToday:0};return J(d)}
  if(/^\/sessions\/[^/]+\/messages/.test(path))return J({messages:msgs})
  if(/^\/sessions\/[^/]+\/suggest/.test(path))return J({suggestion:''})
  if(/^\/sessions\/[^/]+$/.test(path)){const s=sessions.find(x=>path.endsWith(x.sessionId))||sessions[0];return J(Object.assign({},s,s.sessionId===SA&&ask?{status:'needsInput',activityType:'waiting',prompt:ask}:{},{timeline:[{at:iso(8),activityType:'testing',tool:'Bash'},{at:iso(15),activityType:'coding',tool:'Edit'}]}))}
  if(path==='/accounts')return J({accounts:[{provider:'claude',loggedIn:true,method:'claude.ai',email:'you@example.com',org:'Personal'},{provider:'codex',loggedIn:true,method:'ChatGPT'},{provider:'agy',loggedIn:false}]})
  if(path==='/tokens')return J({tokens:[{id:'a1',name:A,osUser:A,createdAt:iso(3000)},{id:'b2',name:B,osUser:B,createdAt:iso(900)}],osUsers:true})
  if(path==='/vault')return J({entries:VAULT})
  if(path==='/rooms')return J({policy:{enabled:true,weeklyPct:80},model:'opus',lowModel:'claude-haiku-5-5',weekly:12})
  if(path==='/clis')return J({clis:[{name:'claude',version:'2.1',method:'native'},{name:'codex',version:'0.172',method:'npm'},{name:'agy',version:'1.5',method:'manual'}],update:{running:false,results:[]}})
  if(path==='/reviews')return J({reviews:[]});if(path==='/models')return J({models:[],codex:[]});if(path==='/policy')return J({policy:{}});if(path.startsWith('/tasks'))return J({tasks:[]})
  return J({})})
 await p.addInitScript(l=>{try{localStorage.setItem('aiagentLang',l)}catch(e){}},lang)
 await p.goto(BASE+'/aiagent');await p.waitForTimeout(900);return p}
const shot=(p,n,o={})=>p.screenshot(Object.assign({path:OUT+n+'.jpg',type:'jpeg',quality:78},o))
const el=(loc,n,pad=12)=>loc.evaluate(e=>{e.scrollIntoView({block:'center'});const r=e.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height}}).then(r=>loc.page().screenshot({path:OUT+n+'.jpg',type:'jpeg',quality:80,clip:{x:Math.max(0,r.x-pad),y:Math.max(0,r.y-pad),width:r.width+2*pad,height:r.height+2*pad}}))
const login=async p=>{await p.fill('#loginToken',TOKEN||'kaw_demo');await p.click('#loginForm button[type=submit]');await p.waitForTimeout(3200)}
const pick=async(p,name)=>{await p.click('#viewList');await p.waitForTimeout(900);await p.locator('.sCards b',{hasText:name}).first().click();await p.waitForTimeout(1800)}
const V={width:1440,height:810}
let p=await page(V)
if(role==='admin'){await shot(p,'login');await login(p)
 await p.click('#viewMap');await p.waitForTimeout(2500);await shot(p,'village')
 await p.evaluate(()=>{st.house=HOUSE;st.roomFocus='lounge';st.sel=null;render();const s=document.createElement('style');s.textContent='.skyLayer{display:none!important}';document.head.append(s)});await p.waitForTimeout(6000);await shot(p,'room')
 await pick(p,A);await shot(p,'list-admin')
 await p.click('.qBadge');await p.waitForTimeout(400);await shot(p,'quota',{clip:{x:560,y:0,width:880,height:330}});await p.mouse.click(300,500)
 await p.click('#accountsBtn');await p.waitForTimeout(2500);await shot(p,'accounts')
 await p.evaluate(()=>{for(const d of document.querySelectorAll('#accountsDlg, #accountsDlg *'))if(d.scrollHeight>d.clientHeight+20)d.scrollTop=d.scrollHeight});await p.waitForTimeout(600);await shot(p,'accounts-rooms')
 // the vault: names and purposes only; a refused save shows why and keeps what was typed
 await p.locator('#vaultBody').scrollIntoViewIfNeeded();await p.fill('#vaultName','gitlab-ci');await p.fill('#vaultPurpose','อ่าน repo ให้ CI');await p.fill('#vaultValue','short')
 await p.locator('#vaultBody button.pri').click();await p.waitForTimeout(700);await el(p.locator('#vaultBody'),'vault')
}else{await login(p);await pick(p,A);await shot(p,'list-user')
 await p.locator('.sCards b',{hasText:B}).first().click();await p.waitForTimeout(1500);await shot(p,'mate')
 // answering the agent's questions from the web
 ask=ASK;await pick(p,A);await p.waitForTimeout(2500)
 await p.locator('.askBox input[type=radio]').first().check();await p.locator('.askBox input[type=checkbox]').nth(0).check();await p.locator('.askBox input[type=checkbox]').nth(1).check();await p.waitForTimeout(300)
 await el(p.locator('.askBox'),'ask')
 ask=ASK_SUBMIT;await p.waitForTimeout(3500);await el(p.locator('.askBox'),'ask-submit');ask=null;await p.waitForTimeout(3000)
 // one send at a time; a send the service refused as a repeat; a send no transcript confirmed
 const send=async(text)=>{await p.fill('#askText',text);await p.click('#askGo');await p.waitForTimeout(500);if(await p.locator('#confirm[open]').count()){await p.click('#confirmOk');await p.waitForTimeout(600)}}
 post='hold';await send('ช่วยทำให้ตารางไม่ล้นจอบนมือถือ แล้วแนบภาพก่อนและหลัง');await p.waitForTimeout(400);await shot(p,'sending')
 await p.close();p=await page(V);await login(p);await pick(p,A)
 post='dup';await send('ช่วยทำให้ตารางไม่ล้นจอบนมือถือ แล้วแนบภาพก่อนและหลัง');await p.waitForTimeout(900);await el(p.locator('#confirm .dlg'),'resend',16)
 await p.click('#confirm button[value=cancel]');await p.waitForTimeout(500)
 post='lost';await send('สรุปสถานะล่าสุดให้หน่อย');await p.waitForTimeout(1500);await shot(p,'not-confirmed');post=''
 // a review page judged item by item
 if(TOKEN){await p.close();p=await page(V);await login(p);await p.click('#viewReview');await p.waitForTimeout(1500)
  const want=process.env.REVIEW;const card=want?p.locator('.revItem',{hasText:want}).first():p.locator('.revItem').first()
  await card.click();await p.waitForTimeout(3000)
  const f=p.frameLocator('#reviewFrame');const ok=f.getByRole('button',{name:'ผ่าน'}),fix=f.getByRole('button',{name:'ต้องแก้ไข'})
  await ok.nth(0).click();await fix.nth(1).click();await f.locator('textarea').nth(1).fill('ปุ่มบันทึกเล็กไปบนมือถือ ขอสูงอย่างน้อย 44px');await ok.nth(2).click();await p.waitForTimeout(800)
  await shot(p,'review')}
 const m=await page({width:390,height:844});await login(m);await m.waitForTimeout(800);await shot(m,'mobile')
 // the room's page on its own host: widths, capture, send to review
 const c=await b.newContext({viewport:V,deviceScaleFactor:2,locale:'th-TH'}),pv=await c.newPage()
 const cap=async w=>{const t=await (await b.newContext({viewport:{width:w,height:w<500?844:900}})).newPage();await t.setContent(DEMO);const x=(await t.screenshot({type:'jpeg',quality:70})).toString('base64');await t.context().close();return {width:w,data:x}}
 const imgs=[await cap(390),await cap(1440)]
 await pv.route('http://preview.example/**',r=>{const u=new URL(r.request().url()),f=u.pathname.replace('/dev/'+A,'')
  if(f==='/'||f==='')return r.fulfill({contentType:'text/html;charset=utf-8',body:readFileSync(SHELL+'shell.html','utf8')})
  if(f==='/-/shell.css'||f==='/-/shell.js')return r.fulfill({contentType:f.endsWith('css')?'text/css':'text/javascript',body:readFileSync(SHELL+f.slice(3),'utf8')})
  const J=d=>r.fulfill({contentType:'application/json',body:JSON.stringify({code:'SUCCESS',details:d})})
  if(f==='/-/state')return J({source:{kind:'static',folder:'my-app/site',version:'1'},capture:true,base:'/dev/'+A+'/~demo/'})
  if(f==='/-/capture')return J({id:'c1',images:imgs})
  if(f==='/-/review')return J({})
  if(f.startsWith('/~demo/'))return r.fulfill({contentType:'text/html;charset=utf-8',body:DEMO})
  return r.fulfill({status:404,body:''})})
 await pv.goto('http://preview.example/dev/'+A+'/');await pv.waitForTimeout(1500);await pv.click('[data-w="390"]');await pv.waitForTimeout(600);await shot(pv,'preview')
 await pv.click('#capture');await pv.waitForTimeout(1200);await pv.fill('#note','ตรวจหน้ารายการทั้งสองขนาด');await pv.click('#send');await pv.waitForTimeout(600);await shot(pv,'preview-capture')}
await b.close()
