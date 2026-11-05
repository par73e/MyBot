const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {JSDOM}=require('jsdom');
const tick=()=>new Promise(r=>setTimeout(r,0));
async function loginPage(){
 const dom=new JSDOM('<div id="notice"></div><div id="root"></div>',{url:'http://localhost/asrobot/',runScripts:'outside-only'});
 const w=dom.window, pending=[], logins=[];
 w.fetch=async(url,options={})=>{
  if(String(url).includes('/login/captcha'))return new Promise(resolve=>pending.push({url:String(url),options,reply:(key,expires=300)=>resolve({ok:true,json:async()=>({key,image:'data:image/png;base64,aGVsbG8=',expires_in:expires})})}));
  if(String(url).endsWith('/login')){logins.push(JSON.parse(options.body));return {ok:false,status:400,json:async()=>({detail:'验证码错误或已过期，请重新获取'})};}
  return {ok:false,status:401,json:async()=>({detail:'请先登录'})};
 };
 for(const file of ['native.js','robot-pages.js','app.js'])vm.runInContext(fs.readFileSync(path.join(__dirname,'../app/static',file),'utf8'),dom.getInternalVMContext());
 await tick();
 return {w,dom,pending,logins,form:()=>w.document.querySelector('#auth-form'),submit:async()=>{
  const form=w.document.querySelector('#auth-form');form.dispatchEvent(new w.SubmitEvent('submit',{bubbles:true,cancelable:true,submitter:form.querySelector('[type=submit]')}));await tick();
 }};
}
test('refresh invalidates old challenge immediately and cannot submit an expired key while loading',async()=>{
 const b=await loginPage();try{
  b.pending.shift().reply('expired-key');await tick();
  b.form().elements.login.value='alice';b.form().elements.password.value='secret';b.form().elements.captcha_code.value='2222';
  b.w.document.querySelector('#captcha-refresh').click();
  assert.equal(b.form().elements.captcha_key.value,'');
  assert.equal(b.form().querySelector('[type=submit]').disabled,true);
  await b.submit();assert.equal(b.logins.length,0);
  const request=b.pending.shift();assert.equal(request.options.cache,'no-store');request.reply('fresh-key');await tick();
  assert.equal(b.form().elements.captcha_key.value,'fresh-key');assert.equal(b.form().elements.captcha_code.value,'');
  assert.equal(b.form().querySelector('[type=submit]').disabled,false);
  b.form().elements.captcha_code.value='2222';await b.submit();assert.equal(b.logins[0].captcha_key,'fresh-key');
 }finally{b.dom.window.close();}
});
test('an old login form cannot overwrite a newly rendered challenge and successful refresh clears expired error',async()=>{
 const b=await loginPage();try{
  const old=b.pending.shift();b.w.eval('authPage()');const latest=b.pending.shift();
  latest.reply('new-form-key');await tick();old.reply('old-form-key');await tick();
  assert.equal(b.form().elements.captcha_key.value,'new-form-key');
  b.w.eval("notice('验证码错误或已过期，请重新获取')");
  b.w.document.querySelector('#captcha-refresh').click();b.pending.shift().reply('refreshed-key');await tick();
  assert.doesNotMatch(b.w.document.querySelector('#notice').textContent,/过期/);
 }finally{b.dom.window.close();}
});

test('an aged page invalidates the expired challenge and manual refresh restores login readiness',async()=>{
 const b=await loginPage();try{
  b.pending.shift().reply('expiring-key',1);await tick();await tick();
  assert.equal(b.form().elements.captcha_key.value,'');
  assert.equal(b.form().querySelector('[type=submit]').disabled,true);
  b.w.document.querySelector('#captcha-refresh').click();b.pending.shift().reply('after-expiry');await tick();
  assert.equal(b.form().elements.captcha_key.value,'after-expiry');
  assert.equal(b.form().querySelector('[type=submit]').disabled,false);
 }finally{b.dom.window.close();}
});
