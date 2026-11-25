// Screenshots for the neutral guide: the real page, with every API answer
// replaced by made-up data, so no real name, host or conversation is shown.
import { chromium } from 'playwright'
// needs a running `aiagent serve --listen 127.0.0.1:8999` (any state dir, enabled)
const OUT=new URL('./img/', import.meta.url).pathname, role=process.env.ROLE||'admin'
const now=Date.now(), iso=m=>new Date(now-m*60000).toISOString()
const S=(id,name,o={})=>Object.assign({sessionId:id,runtimeType:'claudeCode',providerId:'anthropic',displayName:name,owner:name,cwd:'',repoId:'project',status:'idle',live:true,liveEvidence:'process',activityType:'idle',startedAt:iso(180),lastEventAt:iso(4),usage:{inputTokens:39000,outputTokens:5000,source:'actual'},kind:'bg',messageVia:'agcmd',dispatchVia:name,stopVia:'agcmdRestart',model:'claude-opus-5-5',gitBranch:'feat/login'},o)
const sessions= role==='admin' ? [
 S('11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa','main',{protected:true,status:'running',activityType:'planning',messageVia:'relay',dispatchVia:'',look:'cx-kid6',lastEventAt:iso(0)}),
 S('22222222-aaaa-4aaa-8aaa-aaaaaaaaaaaa','alice',{look:'dev',status:'running',activityType:'coding',lastEventAt:iso(1)}),
 S('33333333-aaaa-4aaa-8aaa-aaaaaaaaaaaa','bob',{look:'cx-fox',status:'needsInput',lastEventAt:iso(6)}),
 S('codex-proc-1','Codex',{runtimeType:'codexCli',providerId:'openai',look:'cx-bot3',dispatchVia:'codex',kind:'interactive',model:''}),
 S('agy-agy','Antigravity',{runtimeType:'agyCli',providerId:'google',look:'cx-kid1',dispatchVia:'agy',kind:'interactive',model:''}),
] : [
 S('22222222-aaaa-4aaa-8aaa-aaaaaaaaaaaa','alice',{look:'dev',status:'running',activityType:'coding',lastEventAt:iso(1)}),
 S('33333333-aaaa-4aaa-8aaa-aaaaaaaaaaaa','bob',{look:'cx-fox',readOnly:true,messageVia:'',dispatchVia:'',stopVia:'',messageBlocked:'ห้องของ bob — อ่านบทสนทนาได้ ส่งข้อความไม่ได้',stopBlocked:'x',lastEventAt:iso(6)}),
 {sessionId:'11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa',runtimeType:'claudeCode',providerId:'anthropic',displayName:'admin',status:'running',live:true,liveEvidence:'process',activityType:'unknown',startedAt:iso(200),lastEventAt:iso(0),kind:'bg',protected:true,look:'cx-kid6',messageBlocked:'session ของผู้ดูแล',stopBlocked:'x',usage:{}},
]
const q=(p,w,u,r)=>({providerId:p,mode:u>80?'critical':'normal',confidence:'actual',usedPercent:u,remainingPercent:100-u,windowMinutes:w,resetsAt:new Date(now+r*3600000).toISOString(),observedAt:iso(2),source:'plan'})
const msgs=[
 {role:'user',at:iso(22),text:'เพิ่มปุ่ม "ลืมรหัสผ่าน" ในหน้า login\n\nเสร็จเมื่อ: กดแล้วไปหน้า /reset และ test ผ่าน\nห้ามแตะ: ไฟล์ config'},
 {role:'assistant',at:iso(21),text:'รับทราบครับ แผนคือ\n1. เพิ่มปุ่มใน LoginForm\n2. เพิ่ม route /reset\n3. เขียน test 2 ข้อ\n\nเริ่มทำเลยครับ'},
 {role:'assistant',at:iso(6),text:'เสร็จแล้วครับ\n- แก้ 3 ไฟล์ใน src/login\n- test ผ่าน 14/14\n- commit a1b2c3d บน branch feat/login\n\nรอคุณตรวจที่หน้ารีวิว'},
 {role:'user',at:iso(4),text:'ดีครับ ขอภาพหน้าจอก่อนและหลังด้วย'},
]
const b=await chromium.launch()
async function page(vp,lang='th'){const c=await b.newContext({viewport:vp,deviceScaleFactor:vp.width<500?3:2,locale:lang==='th'?'th-TH':'en-US',timezoneId:process.env.TZID||'Asia/Bangkok'}),p=await c.newPage()
 await p.route('**/aiagent/api/**',async r=>{const u=new URL(r.request().url()),path=u.pathname.replace('/aiagent/api','');const J=d=>r.fulfill({contentType:'application/json',body:JSON.stringify({code:'SUCCESS',details:d})})
  if(r.request().method()!=='GET')return r.abort()
  if(path==='/me')return J({role, name:role==='user'?'alice':undefined, osUser:role==='user'?'alice':undefined})
  if(path==='/snapshot'){const d={generatedAt:new Date().toISOString(),version:'0.15.0'};d.host='my-server';d.sessions=sessions;d.runs=[];d.warnings=[];d.pending=[];d.workspaces=role==='admin'?[{name:'codex',tmuxSession:'site:codex',dir:'/srv/ai-work/codex',dirExists:true,tmuxAlive:true,agentCommand:'codex',agentReady:true},{name:'agy',tmuxSession:'site:agy',dir:'/srv/ai-work/agy',dirExists:true,tmuxAlive:true,agentCommand:'agy',agentReady:true}]:[{name:'alice',tmuxSession:'core:alice',dir:'/home/alice/work',dirExists:true,tmuxAlive:true,agentCommand:'claude',agentReady:true}]
   d.quotas=[q('anthropic',300,24,3),q('anthropic',10080,12,96),q('openai',300,9,2),q('openai',10080,30,50),q('google',300,2,4),q('google',10080,1,160)];d.summary={running:role==='admin'?2:2,needsInput:role==='admin'?1:0,completed:0,idle:role==='admin'?2:1,waitingApproval:0,lost:0,endedToday:0,runsDoneToday:0,runsAbortedToday:0};return J(d)}
  if(/^\/sessions\/[^/]+\/messages/.test(path))return J({messages:msgs})
  if(/^\/sessions\/[^/]+\/suggest/.test(path))return J({suggestion:''})
  if(/^\/sessions\/[^/]+$/.test(path)){const s=sessions.find(x=>path.endsWith(x.sessionId))||sessions[0];return J(Object.assign({},s,{timeline:[{at:iso(8),activityType:'testing',tool:'Bash'},{at:iso(15),activityType:'coding',tool:'Edit'}]}))}
  if(path==='/accounts')return J({accounts:[{provider:'claude',loggedIn:true,method:'claude.ai',email:'you@example.com',org:'Personal'},{provider:'codex',loggedIn:true,method:'ChatGPT'},{provider:'agy',loggedIn:false}]})
  if(path==='/tokens')return J({tokens:[{id:'a1',name:'alice',osUser:'alice',createdAt:iso(3000)},{id:'b2',name:'bob',osUser:'bob',createdAt:iso(900)}],osUsers:true})
  if(path==='/rooms')return J({policy:{enabled:true,weeklyPct:80},model:'opus',lowModel:'claude-haiku-5-5',weekly:12})
  if(path==='/clis')return J({clis:[{name:'claude',version:'2.1',method:'native'},{name:'codex',version:'0.172',method:'npm'},{name:'agy',version:'1.5',method:'manual'}],update:{running:false,results:[]}})
  if(path==='/reviews')return J({reviews:[]});if(path==='/models')return J({models:[],codex:[]});if(path==='/policy')return J({policy:{}});if(path.startsWith('/tasks'))return J({tasks:[]})
  return J({})})
 await p.addInitScript(l=>{try{localStorage.setItem('aiagentLang',l)}catch(e){}},lang)
 await p.goto('http://127.0.0.1:8999/aiagent');await p.waitForTimeout(900);return p}
const shot=(p,n,o={})=>p.screenshot(Object.assign({path:OUT+n+'.jpg',type:'jpeg',quality:78},o))
const login=async p=>{await p.fill('#loginToken','kaw_demo');await p.click('#loginForm button[type=submit]');await p.waitForTimeout(3200)}
const V={width:1440,height:810}
let p=await page(V)
if(role==='admin'){await shot(p,'login');await login(p)
 await p.click('#viewMap');await p.waitForTimeout(2500);await shot(p,'village')
 await p.evaluate(()=>{st.house=HOUSE;st.roomFocus='lounge';st.sel=null;render();const s=document.createElement('style');s.textContent='.skyLayer{display:none!important}';document.head.append(s)});await p.waitForTimeout(6000);await shot(p,'room')
 await p.click('#viewList');await p.waitForTimeout(900);await p.locator('.sCards b',{hasText:'alice'}).first().click();await p.waitForTimeout(1800);await shot(p,'list-admin')
 await p.click('.qBadge');await p.waitForTimeout(400);await shot(p,'quota',{clip:{x:560,y:0,width:880,height:330}});await p.mouse.click(300,500)
 await p.click('#accountsBtn');await p.waitForTimeout(2500);await shot(p,'accounts')
 await p.evaluate(()=>{for(const d of document.querySelectorAll('#accountsDlg, #accountsDlg *'))if(d.scrollHeight>d.clientHeight+20)d.scrollTop=d.scrollHeight});await p.waitForTimeout(600);await shot(p,'accounts-rooms')
}else{await login(p);await p.click('#viewList');await p.waitForTimeout(900);await p.locator('.sCards b',{hasText:'alice'}).first().click();await p.waitForTimeout(1800);await shot(p,'list-user')
 await p.locator('.sCards b',{hasText:'bob'}).first().click();await p.waitForTimeout(1500);await shot(p,'mate')
 const m=await page({width:390,height:844});await login(m);await m.waitForTimeout(800);await shot(m,'mobile')}
await b.close()
