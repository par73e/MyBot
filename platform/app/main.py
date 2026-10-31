import asyncio, json, os, re, secrets, io, zipfile, sqlite3, base64, mimetypes
from pathlib import Path
from contextlib import asynccontextmanager
from datetime import datetime, timedelta
from argon2 import PasswordHasher
from argon2.exceptions import VerificationError, InvalidHashError
from fastapi import FastAPI, Request, HTTPException
from fastapi.responses import JSONResponse, FileResponse, Response
from fastapi.staticfiles import StaticFiles
import qrcode
from . import store as s, runtime as rt, history, native, instance_history, instance_pairing
from .security import password_work

# Minimal Linux images may not include the WebP system MIME registration.
mimetypes.add_type('image/webp','.webp')

ORIGIN=os.environ.get('ASROBOT_ORIGIN','http://localhost:18980')
PREFIX='/asrobot'
HOST_COOKIE=os.environ.get('ASROBOT_HOST_COOKIE')=='1'
if HOST_COOKIE and not ORIGIN.startswith('https://'):
    raise RuntimeError('host-prefixed session cookies require HTTPS')
COOKIE_NAME='__Host-asrobot_session' if HOST_COOKIE else 'asrobot_session'
COOKIE_PATH='/' if HOST_COOKIE else PREFIX+'/'
ph=PasswordHasher(time_cost=3,memory_cost=65536,parallelism=2)
dummy=ph.hash(secrets.token_urlsafe(32))
@asynccontextmanager
async def lifespan(app):
    s.init(); task=asyncio.create_task(rt.supervisor())
    yield
    task.cancel(); await asyncio.gather(task,return_exceptions=True)
app=FastAPI(lifespan=lifespan,docs_url=None,redoc_url=None,openapi_url=None)

@app.middleware('http')
async def security(request,call_next):
    response=None
    if request.method in ('POST','PUT','PATCH','DELETE'):
        if request.headers.get('origin')!=ORIGIN:response=JSONResponse({'detail':'请求来源不匹配，请刷新页面'},403)
        try:
            length=int(request.headers.get('content-length','0') or 0)
            if length<0:raise ValueError()
        except ValueError:response=JSONResponse({'detail':'请求长度格式错误'},400)
        else:
            if length>65536:response=JSONResponse({'detail':'请求内容过长'},413)
    if response is None:
        try:response=await call_next(request)
        except Exception:response=JSONResponse({'detail':'服务暂时不可用，请稍后重试'},500)
    # Cache only public assets; authenticated pages and APIs remain private.
    cache='no-store'
    if request.method in ('GET','HEAD') and response.status_code in (200,206,304) and request.url.path.startswith(PREFIX+'/static/'):
        cache='public, max-age=0, must-revalidate'
        if re.fullmatch(PREFIX+r'/static/home-background-(desktop|mobile)\.[0-9a-f]{12}\.webp',request.url.path):
            cache='public, max-age=31536000, immutable'
    response.headers.update({'Cache-Control':cache,'X-Content-Type-Options':'nosniff','Referrer-Policy':'no-referrer','X-Frame-Options':'DENY','Permissions-Policy':'camera=(), microphone=(), geolocation=()','Content-Security-Policy':"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"})
    return response

@app.exception_handler(ValueError)
async def bad_value(req,exc): return JSONResponse({'detail':str(exc)[:200]},400)
@app.exception_handler(native.NativeError)
async def native_error(req,exc):return JSONResponse({'detail':str(exc)[:200]},exc.status)

def auth(req,admin=False):
    token=req.cookies.get(COOKIE_NAME,'')
    with s.db() as c: u=c.execute('SELECT u.*,ss.csrf,ss.id AS session_id FROM sessions ss JOIN users u ON ss.user_id=u.id WHERE ss.id=? AND ss.expires>? AND u.status=\'active\'',(s.digest(token),s.now())).fetchone()
    if not u: raise HTTPException(401,'请先登录')
    if req.method!='GET' and not secrets.compare_digest(req.headers.get('x-csrf-token',''),u['csrf']): raise HTTPException(403,'页面已过期，请刷新重试')
    if u['must_change'] and req.url.path not in (PREFIX+'/api/me',PREFIX+'/api/password',PREFIX+'/api/logout'): raise HTTPException(403,'请先修改初始密码')
    if admin and u['role']!='admin': raise HTTPException(403,'需要管理员权限')
    if os.environ.get('ASROBOT_AUTOPROVISION') == '1' and not admin and req.url.path not in (PREFIX+'/api/me',PREFIX+'/api/workspace',PREFIX+'/api/password',PREFIX+'/api/logout'):
        with s.db() as c:
            pending=c.execute("SELECT 1 FROM assistant_instances i JOIN workspaces w ON w.user_id=i.user_id WHERE i.user_id=? AND i.state='reserved' AND w.legacy_source IS NULL AND w.native_source IS NULL",(u['id'],)).fetchone()
        if pending: raise HTTPException(503,'您的助手正在准备，请稍后刷新页面')
    return dict(u)
def public_user(u): return {k:u[k] for k in ('id','login','role','status','must_change','created')}
def password_ok(value):
    if not isinstance(value,str) or not value: raise ValueError('请输入密码')
    if len(value.encode('utf-8'))>4096:raise ValueError('密码内容过长')
def verify(encoded,value):
    try: return ph.verify(encoded,value)
    except (VerificationError,InvalidHashError,TypeError): return False
async def body(req):
    raw=bytearray()
    async for chunk in req.stream():
        if len(raw)+len(chunk)>65536:raise HTTPException(413,'请求内容过长')
        raw.extend(chunk)
    try:
        result=json.loads(raw)
        if not isinstance(result,dict): raise ValueError()
        return result
    except (ValueError,TypeError): raise HTTPException(400,'请求格式错误')

@app.get(PREFIX+'/api/health')
def health(): return {'status':'ok','version':'0.6.0','registration':'invite-only'}
@app.post(PREFIX+'/api/register')
async def register(req:Request):
    b=await body(req)
    if s.limited('register:'+req.client.host,5,600): raise HTTPException(429,'尝试过多，请稍后重试')
    if not isinstance(b.get('login',''),str): raise ValueError('用户名格式错误')
    login=b.get('login','').strip().lower(); password=b.get('password',''); password_ok(password)
    if not re.fullmatch(r'[a-z0-9][a-z0-9_.-]{2,39}',login): raise ValueError('用户名使用 3–40 位英文字母、数字、点、横线或下划线')
    if not b.get('agree'): raise ValueError('请确认已阅读数据说明')
    invite_value=b.get('invite','')
    if not isinstance(invite_value,str) or len(invite_value)>128:raise ValueError('邀请码无效、已使用或已过期')
    invite_hash=s.digest(invite_value)
    with s.db() as c:
        if not c.execute('SELECT 1 FROM invites WHERE hash=? AND used=0 AND expires>?',(invite_hash,s.now())).fetchone():raise ValueError('邀请码无效、已使用或已过期')
    encoded=await password_work(ph.hash,password)
    with s.db() as c:
        c.execute('BEGIN IMMEDIATE')
        invite=c.execute('SELECT * FROM invites WHERE hash=? AND used=0 AND expires>?',(invite_hash,s.now())).fetchone()
        if not invite: raise ValueError('邀请码无效、已使用或已过期')
        uid=s.ident()
        try: c.execute('INSERT INTO users(id,login,password,role,created) VALUES(?,?,?,?,?)',(uid,login,encoded,invite['role'],s.now()))
        except sqlite3.IntegrityError: raise ValueError('该用户名不可用')
        c.execute('UPDATE invites SET used=1 WHERE hash=?',(invite['hash'],))
        c.execute('INSERT INTO workspaces(id,user_id,created) VALUES(?,?,?)',(s.ident(),uid,s.now()))
        if os.environ.get('ASROBOT_AUTOPROVISION') == '1' and invite['role'] != 'admin':
            c.execute("INSERT INTO assistant_instances(id,user_id,created,state) VALUES(?,?,?,'reserved')",(s.ident(),uid,s.now()))
    s.workspace(uid); s.event(uid,'账号注册'); return {'ok':True}
@app.post(PREFIX+'/api/login')
async def login(req:Request):
    b=await body(req)
    if s.limited('login:'+req.client.host,15,300): raise HTTPException(429,'登录尝试过多，请五分钟后重试')
    login_name=b.get('login','');value=b.get('password','')
    if not isinstance(login_name,str) or len(login_name)>128 or not isinstance(value,str) or not value or len(value.encode('utf-8'))>4096:raise HTTPException(401,'用户名或密码不正确，或账号已停用')
    login_name=login_name.strip().lower();account_limit='login-account:'+s.digest(login_name)
    if s.limited(account_limit,10,300):raise HTTPException(429,'登录尝试过多，请五分钟后重试')
    with s.db() as c:u=c.execute('SELECT * FROM users WHERE login=?',(login_name,)).fetchone()
    ok=await password_work(verify,u['password'] if u else dummy,value)
    if not ok or not u or u['status']!='active': raise HTTPException(401,'用户名或密码不正确，或账号已停用')
    token=secrets.token_urlsafe(40); csrf=secrets.token_urlsafe(32)
    with s.db() as c:
        c.execute('BEGIN IMMEDIATE')
        current=c.execute('SELECT * FROM users WHERE id=?',(u['id'],)).fetchone()
        if not current or current['status']!='active' or current['password']!=u['password']: raise HTTPException(401,'账号状态已变化，请重新登录')
        u=current
        c.execute('DELETE FROM sessions WHERE expires<?',(s.now(),))
        c.execute('INSERT INTO sessions VALUES(?,?,?,?)',(s.digest(token),u['id'],csrf,s.now()+604800))
        c.execute('DELETE FROM limits WHERE key=?',(account_limit,))
    s.workspace(u['id'])
    r=JSONResponse({'user':public_user(u),'csrf':csrf})
    r.set_cookie(COOKIE_NAME,token,max_age=604800,httponly=True,secure=ORIGIN.startswith('https:'),samesite='strict',path=COOKIE_PATH)
    s.event(u['id'],'登录'); return r
@app.get(PREFIX+'/api/me')
def me(req:Request):
    u=auth(req); return {'user':public_user(u),'csrf':u['csrf']}
@app.post(PREFIX+'/api/logout')
def logout(req:Request):
    u=auth(req)
    with s.db() as c: c.execute('DELETE FROM sessions WHERE id=?',(u['session_id'],))
    r=JSONResponse({'ok':True}); r.delete_cookie(COOKIE_NAME,path=COOKIE_PATH,secure=HOST_COOKIE,httponly=True,samesite='strict'); return r

@app.post(PREFIX+'/api/password')
async def password(req:Request):
    u=auth(req); b=await body(req); password_ok(b.get('password'))
    if s.limited('password-confirm:'+u['id'],8,300):raise HTTPException(429,'密码确认尝试过多，请五分钟后重试')
    password_ok(b.get('current'))
    if not await password_work(verify,u['password'],b['current']): raise ValueError('当前密码不正确')
    encoded=await password_work(ph.hash,b['password'])
    with s.db() as c:
        c.execute('BEGIN IMMEDIATE')
        auth(req)
        current=c.execute('SELECT password FROM users WHERE id=?',(u['id'],)).fetchone()
        if not current or current[0]!=u['password']: raise HTTPException(401,'登录凭据已变化，请重新登录')
        c.execute('UPDATE users SET password=?,must_change=0 WHERE id=?',(encoded,u['id']))
        c.execute('DELETE FROM sessions WHERE user_id=?',(u['id'],))
    return {'ok':True,'message':'密码已更新，请重新登录'}
@app.delete(PREFIX+'/api/account')
async def delete_account(req:Request):
    u=auth(req); b=await body(req)
    if s.limited('password-confirm:'+u['id'],8,300):raise HTTPException(429,'密码确认尝试过多，请五分钟后重试')
    password_ok(b.get('password'))
    if b.get('confirm')!=u['login'] or not await password_work(verify,u['password'],b['password']): raise ValueError('请填写正确的用户名和密码确认注销')
    async with rt.lock(u['id']):
        auth(req)
        source=s.native(u['id'])
        if source and source.startswith('instance:'):
            raise HTTPException(409,'当前助手的注销与数据清理尚未开放，请联系管理员处理')
        if source:await native.request(u['id'],'pause',{})
        with s.db() as c:
            c.execute('BEGIN IMMEDIATE')
            current=c.execute('SELECT password FROM users WHERE id=?',(u['id'],)).fetchone()
            if not current or current[0]!=u['password']: raise HTTPException(401,'登录凭据已变化，请重新登录')
            if u['role']=='admin' and c.execute("SELECT count(*) FROM users WHERE role='admin' AND status='active'").fetchone()[0]<=1: raise ValueError('最后一位管理员不能注销，请先创建另一管理员')
            c.execute('DELETE FROM users WHERE id=?',(u['id'],))
    return {'ok':True}
@app.get(PREFIX+'/api/settings')
async def get_settings(req:Request):
    u=auth(req); cfg=s.settings(u['id']); cfg['key_configured']=bool(s.secret(u['id'],'model')); cfg['providers']={k:v[0] for k,v in rt.PROVIDERS.items()}; cfg['native_mode']=bool(s.native(u['id']))
    if cfg['native_mode']:
        p=await native.request(u['id'],'profile');provider,model=p['model_ref'].split('/',1)
        current=next((v for v in p['providers'] if v['name']==provider),{})
        cfg.update(prompt=p['prompt'],provider=provider,model=model,reasoning_effort=current.get('default_effort',''),providers={v['name']:v['name'] for v in p['providers']},key_configured=p.get('key_configured',False),migration_info={'source':'original-Friday','scope':'完整原生运行环境','pending_modules':[]})
        if s.native(u['id']).startswith('instance:'):
            identity=await native.request(u['id'],'identity')
            cfg.update(name=identity.get('name') or '助手',address=identity.get('address') or '',migration_info=None)
    return cfg
@app.put(PREFIX+'/api/settings')
async def put_settings(req:Request):
    u=auth(req); b=await body(req); cfg=s.settings(u['id']); old_provider=cfg['provider']
    if s.native(u['id']):raise HTTPException(409,'此账号使用完整原 Friday 配置，请在完整 Friday 页面修改')
    if 'api_key' in b and (not isinstance(b['api_key'],str) or len(b['api_key'])>1024 or (b['api_key'] and not b['api_key'].strip())): raise ValueError('密钥格式错误')
    if b.get('revision')!=cfg['revision']: raise HTTPException(409,'设置已变化，请刷新重试')
    for k,limit in [('name',50),('address',50),('prompt',6000),('style',100),('model',100)]:
        if k in b:
            if not isinstance(b[k],str) or len(b[k])>limit: raise ValueError('设置内容过长')
            cfg[k]=b[k].strip()
    for k in ('routine_start','routine_end'):
        if k in b:
            if not isinstance(b[k],str) or not re.fullmatch(r'(?:[01]\d|2[0-3]):[0-5]\d',b[k]): raise ValueError('作息时间格式错误')
            cfg[k]=b[k]
    if cfg['routine_start']>=cfg['routine_end']: raise ValueError('结束时间必须晚于开始时间，暂不支持跨午夜作息')
    if 'provider' in b:
        if b['provider'] not in ('',*rt.PROVIDERS): raise ValueError('不支持的服务商')
        cfg['provider']=b['provider']
    if 'reasoning_effort' in b:
        if b['reasoning_effort'] not in ('','low','medium','xhigh'): raise ValueError('不支持的思考档位')
        cfg['reasoning_effort']=b['reasoning_effort']
    for k in ('weekly_schedule','date_overrides'):
        if k in b: cfg[k]=b[k]
    rt.validate_schedules(cfg['weekly_schedule'],cfg['date_overrides'])
    if 'modules' in b:
        if not isinstance(b['modules'],dict) or any(k not in cfg['modules'] or not isinstance(v,bool) for k,v in b['modules'].items()): raise ValueError('模块设置格式错误')
        cfg['modules'].update(b['modules'])
    revision=cfg.pop('revision')
    async with rt.lock(u['id']):
        auth(req)
        with s.db() as c:
            if revision==0:
                try: c.execute('INSERT INTO settings VALUES(?,?,1)',(u['id'],json.dumps(cfg)))
                except sqlite3.IntegrityError: raise HTTPException(409,'设置已变化，请刷新重试')
            elif c.execute('UPDATE settings SET body=?,revision=revision+1 WHERE user_id=? AND revision=?',(json.dumps(cfg),u['id'],revision)).rowcount!=1: raise HTTPException(409,'设置已变化，请刷新重试')
            if b.get('api_key'): c.execute('INSERT OR REPLACE INTO secrets VALUES(?,?,?)',(u['id'],'model',s.encrypt(b['api_key'].strip())))
            elif b.get('delete_key') or old_provider!=cfg['provider']: c.execute("DELETE FROM secrets WHERE user_id=? AND kind='model'",(u['id'],))
    s.event(u['id'],'更新助手设置'); return {'ok':True}
@app.post(PREFIX+'/api/model/test')
async def test_model(req:Request):
    u=auth(req)
    if s.limited('modeltest:'+u['id'],3,60): raise HTTPException(429,'测试过于频繁')
    if s.native(u['id']):
        await native.request(u['id'],'chat',{'text':'这是用户点击的模型连接验证。请只回复：连接成功。不要调用工具或创建任何任务、记忆或提醒。'})
        return {'ok':True}
    await rt.model(u['id'],[{'role':'user','content':'请只回复：连接成功'}])
    s.event(u['id'],'模型连接测试'); return {'ok':True}
@app.get(PREFIX+'/api/items/{kind}')
def items(kind:str,req:Request):
    uid=auth(req)['id']
    if s.native(uid):raise HTTPException(409,'此账号已接入原 Friday 的事项与记忆，请刷新后查看')
    return s.entities(uid,kind) if kind in ('task','memory','expiry') else []
@app.post(PREFIX+'/api/items/{kind}')
async def save_item(kind:str,req:Request):
    u=auth(req); b=await body(req)
    if s.native(u['id']):raise HTTPException(409,'此账号使用原 Friday 的事项与记忆，请刷新页面后操作')
    if kind not in ('task','memory','expiry'): raise ValueError('类型不存在')
    async with rt.lock(u['id']):
        auth(req)
        return rt.write_entity(u['id'],kind,b,b.pop('id',None),b.pop('revision',None))
@app.delete(PREFIX+'/api/items/{kind}/{eid}')
async def remove_item(kind:str,eid:str,req:Request):
    u=auth(req); b=await body(req)
    if s.native(u['id']):raise HTTPException(409,'此账号使用原 Friday 的事项与记忆，请刷新页面后操作')
    async with rt.lock(u['id']):
        auth(req)
        with s.db() as c:
            if c.execute('DELETE FROM entities WHERE id=? AND user_id=? AND kind=? AND revision=?',(eid,u['id'],kind,b.get('revision'))).rowcount!=1: raise HTTPException(409,'事项不存在或已变化，请刷新重试')
    return {'ok':True}
@app.post(PREFIX+'/api/chat')
async def chat(req:Request):
    u=auth(req); b=await body(req); text=b.get('text','')
    if not isinstance(text,str) or not 1<=len(text)<=8000: raise ValueError('消息需要 1–8000 字符')
    if s.limited('chat:'+u['id'],20,60): raise HTTPException(429,'消息过于频繁，请稍后重试')
    return {'text':await (native.chat(u['id'],text,authorize=lambda:auth(req)) if s.native(u['id']) else rt.chat(u['id'],text,authorize=lambda:auth(req)))}
@app.get(PREFIX+'/api/messages')
async def messages(req:Request,date:str='',before:int=0,source:str='auto',cursor:str=''):
    u=auth(req)
    if not date:date=(datetime.now(rt.SH)-timedelta(hours=6)).strftime('%Y-%m-%d')
    if before<0:raise ValueError('历史游标无效')
    if source=='instance':
        return await instance_history.messages(u['id'],date,cursor)
    if source=='friday_archive' or (source=='auto' and s.workspace(u['id'])['legacy_source']):
        if not s.workspace(u['id'])['legacy_source']: raise HTTPException(404,'当前工作区未关联旧会话')
        try:
            page=await history.archive(u['id'],'messages',date=date,cursor=cursor,before=before,limit=100)
            if source=='friday_archive' or page['messages'] or page.get('has_more'):
                if source=='auto':
                    page['source']='friday_archive'
                    page['messages']=[dict(m,source='wechat',archive_source='friday_archive') for m in page['messages']]
                return page
        except history.ArchiveUnavailable as exc: raise HTTPException(503,str(exc))
    if source not in ('local','auto'): raise ValueError('会话来源无效')
    if not date: date=(datetime.now(rt.SH)-timedelta(hours=6)).strftime('%Y-%m-%d')
    try: start=datetime.strptime(date,'%Y-%m-%d').replace(hour=6,tzinfo=rt.SH)
    except ValueError: raise ValueError('日期格式应为 YYYY-MM-DD')
    with s.db() as c: rows=c.execute('SELECT id,role,text,source,created FROM messages WHERE user_id=? AND created>=? AND created<? AND (?=0 OR id<?) ORDER BY id DESC LIMIT 101',(u['id'],int(start.timestamp()),int((start+timedelta(days=1)).timestamp()),before,before)).fetchall()
    return {'date':date,'messages':[dict(r) for r in reversed(rows[:100])],'has_more':len(rows)>100,'next_before':rows[99]['id'] if len(rows)>100 else 0}
@app.get(PREFIX+'/api/workspace')
def workspace(req:Request): return s.public_workspace(auth(req)['id'])
@app.get(PREFIX+'/api/native/{section}')
async def native_read(section:str,req:Request):
    uid=auth(req)['id']
    if section not in ('status','tools','profile','identity','mail-accounts','weather-setup','github-credential'):raise HTTPException(404,'页面不存在')
    return await native.request(uid,section)
@app.post(PREFIX+'/api/native/{section}')
async def native_write(section:str,req:Request):
    uid=auth(req)['id'];b=await body(req)
    if section not in ('tool','profile','identity','apply','mail-test','mail-accounts','weather-setup','github-credential'):raise HTTPException(404,'操作不存在')
    if section=='mail-test' and s.limited('nativemail:'+uid,3,60):raise HTTPException(429,'邮箱测试过于频繁，请稍后再试')
    if s.limited('native:'+uid,40,60):raise HTTPException(429,'操作过于频繁，请稍后重试')
    async with rt.lock(uid):
        auth(req)
        result=await native.request(uid,section,b)
    if section=='tool':
        read=b.get('args',{}).get('action') in ('list','get','status','query','search','read','silent','digest_prompt','latest','preview','modules','runs','get_default')
        event=('查看' if read else '修改')+'助手'+native.LABELS.get(b.get('tool'),'模块')
    else:event={'profile':'保存助手配置','identity':'保存助手名称和称呼','apply':'应用助手配置','mail-test':'验证助手邮箱连接','mail-accounts':'保存邮箱连接','weather-setup':'保存天气连接','github-credential':'保存 GitHub 连接'}[section]
    s.event(uid,event)
    return result
@app.get(PREFIX+'/api/conversations')
async def conversations(req:Request):
    uid=auth(req)['id']
    with s.db() as c:
        rows=c.execute("SELECT DISTINCT date(created,'unixepoch','+2 hours') AS date FROM messages WHERE user_id=? ORDER BY date DESC",(uid,)).fetchall()
    result={'workspace':s.public_workspace(uid),'conversations':[{'date':r['date'],'source':'local'} for r in rows],'archive_error':None}
    if result['workspace']['history_linked']:
        try: result['conversations']+=(await history.archive(uid,'days'))['conversations']
        except history.ArchiveUnavailable as exc: result['archive_error']=str(exc)
    if result['workspace']['instance_managed']:
        try: result['conversations']+=await instance_history.days(uid)
        except (native.NativeError,ValueError): result['archive_error']='助手会话暂时无法读取，请稍后重试'
    return result
@app.get(PREFIX+'/api/activity')
def activity(req:Request):
    u=auth(req)
    with s.db() as c: return [dict(r) for r in c.execute('SELECT event,status,created FROM activity WHERE user_id=? ORDER BY id DESC LIMIT 100',(u['id'],))]
@app.get(PREFIX+'/api/wechat')
async def wechat(req:Request):
    u=auth(req)
    if s.native(u['id']):
        d=await native.request(u['id'],'status')
        with s.db() as c: pending=c.execute("SELECT id FROM pairings WHERE user_id=? AND status='installing'",(u['id'],)).fetchone()
        return {'status':'native_running' if d['running'] and d['wechat_configured'] else 'native_stopped','last_poll':0,'native_mode':True,'wechat_configured':bool(d['wechat_configured']),'running':bool(d['running']),'pending_pairing':pending['id'] if pending else None}
    with s.db() as c: r=c.execute('SELECT status,last_poll FROM bindings WHERE user_id=?',(u['id'],)).fetchone()
    return dict(r) if r else {'status':'unbound','last_poll':0}
@app.delete(PREFIX+'/api/wechat')
async def unbind(req:Request):
    u=auth(req)
    if s.native(u['id']):raise HTTPException(409,'原微信连接随完整 Friday 继续使用，请勿通过独立配对入口解绑')
    async with rt.lock(u['id']):
        auth(req)
        with s.db() as c:
            c.execute('DELETE FROM bindings WHERE user_id=?',(u['id'],)); c.execute("DELETE FROM secrets WHERE user_id=? AND kind='wechat'",(u['id'],)); c.execute('DELETE FROM pairings WHERE user_id=?',(u['id'],))
    s.event(u['id'],'解绑微信'); return {'ok':True}
@app.post(PREFIX+'/api/wechat/pairings')
async def start_pairing(req:Request):
    u=auth(req)
    source=s.native(u['id'])
    if source and not source.startswith('instance:'):raise HTTPException(409,'原助手已沿用原微信配置，无需重新扫码')
    if source:
        with s.db() as c:
            if c.execute("SELECT 1 FROM bindings WHERE user_id=?",(u['id'],)).fetchone():raise HTTPException(409,'已有微信绑定或配对正在完成')
            if c.execute('SELECT 1 FROM workspaces WHERE native_source IS NOT NULL LIMIT 1').fetchone():raise HTTPException(409,'原助手的微信归属尚未迁移，请先完成归属核对')
    if s.limited('pair:'+u['id'],3,60): raise HTTPException(429,'二维码申请过于频繁')
    data=await rt.wx_request(rt.WX,'/ilink/bot/get_bot_qrcode',params={'bot_type':3})
    qr=data.get('qrcode'); content=data.get('qrcode_img_content') or qr
    if not qr or not isinstance(content,str) or len(content)>8192: raise ValueError('微信未返回有效二维码')
    pid=s.ident()
    async with rt.lock(u['id']):
        auth(req)
        with s.db() as c:
            c.execute('BEGIN IMMEDIATE')
            if c.execute("SELECT 1 FROM pairings WHERE user_id=? AND status='installing'",(u['id'],)).fetchone():raise ValueError('上次配对正在完成，请重试原二维码')
            if source and c.execute('SELECT 1 FROM bindings WHERE user_id=?',(u['id'],)).fetchone():raise ValueError('已有微信绑定或配对正在完成')
            c.execute('DELETE FROM pairings WHERE user_id=?',(u['id'],))
            c.execute('INSERT INTO pairings VALUES(?,?,?,?,?)',(pid,u['id'],s.encrypt({'qr':qr,'base':rt.WX}), 'wait',s.now()+480))
    image=io.BytesIO(); qrcode.make(content).save(image,format='PNG')
    return {'id':pid,'image':'data:image/png;base64,'+base64.b64encode(image.getvalue()).decode(),'expires':s.now()+480}
@app.post(PREFIX+'/api/wechat/pairings/{pid}')
async def poll_pairing(pid:str,req:Request):
    u=auth(req); b=await body(req)
    with s.db() as c: p=c.execute('SELECT * FROM pairings WHERE id=? AND user_id=?',(pid,u['id'])).fetchone()
    if not p or (p['expires']<s.now() and p['status']!='installing'): raise ValueError('二维码已过期或已取消，请重新申请')
    if p['status']=='confirmed': return {'status':'confirmed'}
    if p['status']=='installing':
        async with rt.lock(u['id']):
            auth(req)
            return await instance_pairing.complete(u['id'],pid)
    data=s.decrypt(p['secret']); params={'qrcode':data['qr']}
    if b.get('verify_code'):
        if not re.fullmatch(r'[0-9]{4,8}',str(b['verify_code'])): raise ValueError('验证码格式错误')
        params['verify_code']=b['verify_code']
    result=await rt.wx_request(data['base'],'/ilink/bot/get_qrcode_status',params=params)
    status=result.get('status','wait')
    if status=='scaned_but_redirect':
        data['base']=rt.wx_url('https://'+result.get('redirect_host',''))
        with s.db() as c: c.execute('UPDATE pairings SET secret=? WHERE id=? AND user_id=?',(s.encrypt(data),pid,u['id']))
    if status=='confirmed':
        if not all(result.get(k) for k in ('bot_token','ilink_bot_id','ilink_user_id')): raise ValueError('微信返回的身份信息不完整')
        source=s.native(u['id'])
        if source and source.startswith('instance:'):
            async with rt.lock(u['id']):
                auth(req)
                return await instance_pairing.complete(u['id'],pid,result)
        binding={'token':result['bot_token'],'base':rt.wx_url(result.get('baseurl') or rt.WX),'user':result['ilink_user_id'],'cursor':'','context':''}
        async with rt.lock(u['id']):
            auth(req)
            with s.db() as c:
                c.execute('BEGIN IMMEDIATE')
                valid=c.execute('SELECT status FROM pairings WHERE id=? AND user_id=? AND expires>?',(pid,u['id'],s.now())).fetchone()
                if not valid: raise ValueError('配对已取消，请重新申请')
                if valid['status']=='confirmed': return {'status':'confirmed'}
                other=c.execute('SELECT user_id FROM bindings WHERE external_id=?',(str(result['ilink_bot_id']),)).fetchone()
                if other and other[0]!=u['id']: raise ValueError('该微信助手已绑定其他平台账号，请先解绑')
                c.execute('INSERT OR REPLACE INTO bindings(user_id,external_id,status) VALUES(?,?,?)',(u['id'],str(result['ilink_bot_id']),'bound'))
                c.execute('INSERT OR REPLACE INTO secrets VALUES(?,?,?)',(u['id'],'wechat',s.encrypt(binding)))
                c.execute("UPDATE pairings SET status='confirmed',secret=? WHERE id=?",(s.encrypt({}),pid))
        s.event(u['id'],'微信配对成功')
    return {'status':status}
@app.delete(PREFIX+'/api/wechat/pairings/{pid}')
def cancel_pairing(pid:str,req:Request):
    u=auth(req)
    with s.db() as c:
        c.execute('BEGIN IMMEDIATE')
        if c.execute("SELECT 1 FROM pairings WHERE id=? AND user_id=? AND status='installing'",(pid,u['id'])).fetchone():raise ValueError('微信已确认，配对正在完成，请稍后重试')
        c.execute('DELETE FROM pairings WHERE id=? AND user_id=?',(pid,u['id']))
    return {'ok':True}
@app.post(PREFIX+'/api/wechat/test')
async def test_wechat(req:Request):
    u=auth(req)
    if s.limited('wxtest:'+u['id'],2,60): raise HTTPException(429,'测试过于频繁')
    if s.native(u['id']):
        await native.request(u['id'],'tool',{'tool':'assistant_tasks','args':{'action':'create','kind':'reminder','title':'微信连接测试','body':'这是一条助手微信连接测试消息。','at':(datetime.now(rt.SH)+timedelta(seconds=15)).isoformat()}})
        s.event(u['id'],'助手微信连接测试已排入提醒')
        return {'ok':True,'message':'测试提醒已交给助手调度，请在微信确认；仍受作息窗口约束'}
    async with rt.lock(u['id']):
        auth(req)
        await rt.send(u['id'],'微信连接测试：您的助手已连接。')
    s.event(u['id'],'微信连接测试','accepted'); return {'ok':True,'message':'微信平台已接受发送请求；请在微信查看'}
@app.post(PREFIX+'/api/export')
async def export(req:Request):
    u=auth(req)
    if s.limited('export:'+u['id'],3,300): raise HTTPException(429,'导出过于频繁')
    workspace=s.public_workspace(u['id'])
    with s.db() as c:
        c.execute('BEGIN')
        cfg=c.execute('SELECT body FROM settings WHERE user_id=?',(u['id'],)).fetchone()
        content={'manifest':{'version':1,'created':s.now(),'scope':'用户内容备份；不含密钥、微信绑定或登录信息；不支持一键恢复'},'settings':json.loads(cfg[0]) if cfg else s.DEFAULT,'items':[{'kind':r['kind'],**json.loads(r['body']),'entity_type':r['kind']} for r in c.execute('SELECT kind,body FROM entities WHERE user_id=?',(u['id'],))],'messages':[dict(r) for r in c.execute('SELECT role,text,source,created FROM messages WHERE user_id=? ORDER BY id',(u['id'],))]}
    content['workspace']=workspace
    content['manifest']['archive_scope']=('旧 Friday 正文保留在原存储，本文件只包含工作区关联说明，不是旧会话的完整备份' if workspace['history_linked'] else '助手的原生会话正文未包含在此文件中；工作区内的网页消息单独包含在 messages 中')
    if s.native(u['id']):
        content['platform_legacy_settings']=content['settings']
        content['settings']={'runtime':'user_instance' if workspace['instance_managed'] else 'original_friday','authoritative_configuration':'native.snapshot 与 native.profile；旧平台投影不参与运行'}
        content['native']={'snapshot':await native.request(u['id'],'status'),'profile':await native.request(u['id'],'profile')}
        for name,tool,args in [('tasks','assistant_tasks',{'action':'list','include_closed':True}),('memories','assistant_memory',{'action':'list','include_inactive':True}),('expiry','assistant_subscriptions',{'action':'list'})]:
            content['native'][name]=await native.request(u['id'],'tool',{'tool':tool,'args':args})
        content['manifest']['native_scope']='助手的脱敏配置、事项与记忆；不含原始凭据和原生会话正文，不支持一键恢复'
    out=io.BytesIO()
    with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED) as z:
        z.writestr('asrobot.json',json.dumps(content,ensure_ascii=False,indent=2)); z.writestr('README.txt','这是用户内容备份。密钥、微信绑定和登录凭据不包含在内。当前版本不提供自动恢复，请保留 JSON 文件供后续受控导入。')
    return Response(out.getvalue(),media_type='application/zip',headers={'Content-Disposition':'attachment; filename="asrobot-backup.zip"'})
@app.get(PREFIX+'/api/admin/users')
def users(req:Request):
    auth(req,True)
    with s.db() as c: return [dict(r) for r in c.execute("SELECT u.id,u.login,u.role,u.status,u.created,CASE WHEN w.native_source IS NOT NULL OR i.id IS NOT NULL THEN 'native_managed' ELSE b.status END AS wechat_status,b.last_poll,(w.native_source IS NOT NULL OR i.id IS NOT NULL) AS native_mode FROM users u LEFT JOIN bindings b ON u.id=b.user_id LEFT JOIN workspaces w ON w.user_id=u.id LEFT JOIN assistant_instances i ON i.user_id=u.id AND i.state='active' ORDER BY u.created")]
@app.post(PREFIX+'/api/admin/invites')
async def invite(req:Request):
    u=auth(req,True); b=await body(req); token=secrets.token_urlsafe(24)
    role=b.get('role','user')
    if role not in ('user','admin'): raise ValueError('无效角色')
    with s.db() as c: c.execute('INSERT INTO invites VALUES(?,?,?,0)',(s.digest(token),role,s.now()+604800))
    s.event(u['id'],'创建测试邀请码'); return {'invite':token,'expires':s.now()+604800}
@app.post(PREFIX+'/api/admin/users/{uid}')
async def manage_user(uid:str,req:Request):
    u=auth(req,True); b=await body(req)
    if uid==u['id']: raise ValueError('不能通过管理入口修改自己')
    async with rt.lock(uid):
        auth(req,True)
        temporary=secrets.token_urlsafe(16) if b.get('action')=='reset' else None
        encoded=await password_work(ph.hash,temporary) if temporary else None
        auth(req,True)
        if b.get('action')=='enable' and os.environ.get('ASROBOT_AUTOPROVISION')=='1' and not s.native(uid):
            with s.db() as c:
                reserved=c.execute("SELECT 1 FROM assistant_instances WHERE user_id=? AND state='reserved'",(uid,)).fetchone()
            if not reserved: raise HTTPException(409,'此账号的助手数据需要先迁移，暂不能启用')
        if b.get('action') in ('disable','enable') and s.native(uid):
            await native.request(uid,'pause' if b['action']=='disable' else 'resume',{})
        with s.db() as c:
            target=c.execute('SELECT role FROM users WHERE id=?',(uid,)).fetchone()
            if not target: raise HTTPException(404,'账号不存在')
            if target[0]=='admin': raise ValueError('不能在此修改其他管理员')
            if b.get('action')=='reset':
                c.execute('UPDATE users SET password=?,must_change=1 WHERE id=?',(encoded,uid))
            elif b.get('action') in ('disable','enable'): c.execute('UPDATE users SET status=? WHERE id=?',('disabled' if b['action']=='disable' else 'active',uid))
            else: raise ValueError('操作不存在')
            c.execute('DELETE FROM sessions WHERE user_id=?',(uid,))
    s.event(u['id'],'管理账号：'+b['action']); return {'ok':True,**({'temporary_password':temporary} if b['action']=='reset' else {})}

STATIC=Path(__file__).parent/'static'
@app.get(PREFIX+'/')
def index(): return FileResponse(STATIC/'index.html')
app.mount(PREFIX+'/static',StaticFiles(directory=str(STATIC)),name='static')
