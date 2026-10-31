import asyncio, io, json, zipfile
from datetime import datetime, timedelta
import pytest
from fastapi.testclient import TestClient
from app import store as s, runtime as rt
from app.main import app, ph

ORIGIN='http://localhost:18980'
@pytest.fixture
def clients(tmp_path,monkeypatch):
    monkeypatch.setattr(s,'DATA',tmp_path)
    monkeypatch.delenv('ASROBOT_KEY_FILE',raising=False)
    rt.locks.clear()
    rt.turn_locks.clear()
    async def idle(): await asyncio.sleep(3600)
    monkeypatch.setattr(rt,'supervisor',idle)
    with TestClient(app,base_url=ORIGIN) as a, TestClient(app,base_url=ORIGIN) as b:
        for client,name,role in [(a,'alice','admin'),(b,'bob','user')]:
            uid=s.ident()
            with s.db() as c: c.execute('INSERT INTO users(id,login,password,role,created) VALUES(?,?,?,?,?)',(uid,name,ph.hash('test-password-123'),role,s.now()))
            r=client.post('/asrobot/api/login',json={'login':name,'password':'test-password-123'},headers={'Origin':ORIGIN})
            assert r.status_code==200,r.text
            client.headers.update({'Origin':ORIGIN,'X-CSRF-Token':r.json()['csrf']})
        yield a,b

def test_tenant_scoping_and_revision(clients):
    a,b=clients
    r=a.post('/asrobot/api/items/task',json={'title':'private'})
    assert r.status_code==200
    eid=r.json()['id']
    assert b.get('/asrobot/api/items/task').json()==[]
    assert b.post('/asrobot/api/items/task',json={'id':eid,'revision':1,'title':'stolen'}).status_code==400
    assert b.request('DELETE','/asrobot/api/items/task/'+eid,json={'revision':1}).status_code==409
    assert a.post('/asrobot/api/items/task',json={'id':eid,'revision':1,'title':'new'}).status_code==200
    assert a.post('/asrobot/api/items/task',json={'id':eid,'revision':1,'title':'stale'}).status_code==400

def test_csrf_role_and_encrypted_secret(clients):
    a,b=clients
    assert b.get('/asrobot/api/admin/users').status_code==403
    assert a.post('/asrobot/api/logout',json={},headers={'X-CSRF-Token':'wrong'}).status_code==403
    assert a.post('/asrobot/api/logout',json={},headers={'Origin':'https://evil.example'}).status_code==403
    cfg=a.get('/asrobot/api/settings').json()
    assert a.put('/asrobot/api/settings',json={'revision':cfg['revision'],'provider':'openai','model':'test-model','api_key':'private-secret'}).status_code==200
    assert 'private-secret' not in (s.DATA/'platform.sqlite3').read_bytes().decode(errors='ignore')
    assert a.get('/asrobot/api/settings').json()['key_configured']
    data=a.post('/asrobot/api/export',json={}).content
    with zipfile.ZipFile(io.BytesIO(data)) as z:
        text=z.read('asrobot.json').decode(); assert 'private-secret' not in text and 'password' not in text

def test_invite_registration_and_single_use(clients):
    a,_=clients
    invite=a.post('/asrobot/api/admin/invites',json={}).json()['invite']
    with TestClient(app,base_url=ORIGIN) as c:
        c.headers['Origin']=ORIGIN
        payload={'login':'charlie','password':'test-password-123','invite':invite,'agree':True}
        assert c.post('/asrobot/api/register',json=payload).status_code==200
        payload['login']='david'
        assert c.post('/asrobot/api/register',json=payload).status_code==400

def test_disable_revoke_and_delete_cascade(clients):
    a,b=clients
    uid=b.get('/asrobot/api/me').json()['user']['id']
    b.post('/asrobot/api/items/memory',json={'title':'secret'})
    assert a.post('/asrobot/api/admin/users/'+uid,json={'action':'disable'}).status_code==200
    assert b.get('/asrobot/api/items/memory').status_code==401
    assert not s.active(uid)
    a.post('/asrobot/api/admin/users/'+uid,json={'action':'enable'})
    r=b.post('/asrobot/api/login',json={'login':'bob','password':'test-password-123'})
    b.headers['X-CSRF-Token']=r.json()['csrf']
    assert b.request('DELETE','/asrobot/api/account',json={'confirm':'bob','password':'test-password-123'}).status_code==200
    with s.db() as c:
        for table in ('entities','sessions','settings','messages','secrets'): assert c.execute('SELECT count(*) FROM '+table+' WHERE user_id=?',(uid,)).fetchone()[0]==0

def test_module_gate_and_host_owned_checkpoint(clients):
    a,_=clients
    cfg=a.get('/asrobot/api/settings').json()
    assert a.put('/asrobot/api/settings',json={'revision':cfg['revision'],'modules':{'tasks':False}}).status_code==200
    assert a.post('/asrobot/api/items/task',json={'title':'disabled'}).status_code==400
    assert a.put('/asrobot/api/settings',json={'revision':1,'modules':{'tasks':True}}).status_code==200
    a.post('/asrobot/api/items/task',json={'title':'safe','notification_state':'accepted'})
    assert 'notification_state' not in a.get('/asrobot/api/items/task').json()[0]

def test_pairing_cancel_and_duplicate_binding(clients,monkeypatch):
    a,b=clients
    async def fake(base,path,**kw):
        if path.endswith('get_bot_qrcode'): return {'qrcode':'private-qr','qrcode_img_content':'https://example.test/qr'}
        return {'status':'confirmed','bot_token':'private-token','ilink_bot_id':'shared-bot','ilink_user_id':'wx-user','baseurl':rt.WX}
    monkeypatch.setattr(rt,'wx_request',fake)
    pair=a.post('/asrobot/api/wechat/pairings',json={}).json()['id']
    assert b.post('/asrobot/api/wechat/pairings/'+pair,json={}).status_code==400
    assert a.post('/asrobot/api/wechat/pairings/'+pair,json={}).status_code==200
    other=b.post('/asrobot/api/wechat/pairings',json={}).json()['id']
    assert b.post('/asrobot/api/wechat/pairings/'+other,json={}).status_code==400
    assert 'private-token' not in a.get('/asrobot/api/wechat').text
    a.delete('/asrobot/api/wechat')
    pair=a.post('/asrobot/api/wechat/pairings',json={}).json()['id']
    a.delete('/asrobot/api/wechat/pairings/'+pair)
    assert a.post('/asrobot/api/wechat/pairings/'+pair,json={}).status_code==400

def test_reminder_at_most_once_and_routine(clients,monkeypatch):
    a,_=clients
    uid=a.get('/asrobot/api/me').json()['user']['id']
    cfg=a.get('/asrobot/api/settings').json()
    a.put('/asrobot/api/settings',json={'revision':cfg['revision'],'routine_start':'00:00','routine_end':'23:59'})
    calls=[]
    async def fake(uid,text): calls.append((uid,text))
    monkeypatch.setattr(rt,'send',fake)
    at=(datetime.now(rt.SH)-timedelta(seconds=5)).isoformat()
    a.post('/asrobot/api/items/task',json={'title':'remind','at':at})
    asyncio.run(rt.tick()); asyncio.run(rt.tick())
    assert len(calls)==1
    item=a.get('/asrobot/api/items/task').json()[0]
    assert item['notification_state']=='accepted' and item.get('status','active')=='active'
    old=(datetime.now(rt.SH)-timedelta(hours=2)).isoformat()
    a.post('/asrobot/api/items/task',json={'title':'missed','at':old})
    asyncio.run(rt.tick())
    assert len(calls)==1

def test_ssrf_and_settings_conflict(clients):
    a,_=clients
    for url in ('http://127.0.0.1','https://169.254.169.254','https://evil.example','https://ilinkai.weixin.qq.com@evil.example'):
        with pytest.raises(ValueError): rt.wx_url(url)
    assert a.put('/asrobot/api/settings',json={'revision':0,'provider':'custom'}).status_code==400
    assert a.put('/asrobot/api/settings',json={'revision':0,'name':'first'}).status_code==200
    assert a.put('/asrobot/api/settings',json={'revision':0,'name':'stale'}).status_code==409

def test_model_credentials_and_context_are_tenant_bound(clients,monkeypatch):
    a,b=clients
    ua=a.get('/asrobot/api/me').json()['user']['id']; ub=b.get('/asrobot/api/me').json()['user']['id']
    seen=[]
    for client,key in [(a,'key-for-alice'),(b,'key-for-bob')]:
        cfg=client.get('/asrobot/api/settings').json()
        client.put('/asrobot/api/settings',json={'revision':cfg['revision'],'provider':'openai','model':'mock-model','api_key':key})
    async def fake_model(uid,messages,tools=False):
        seen.append((uid,s.secret(uid,'model'),json.dumps(messages)))
        return {'role':'assistant','content':'测试回复'}
    monkeypatch.setattr(rt,'model',fake_model)
    assert a.post('/asrobot/api/chat',json={'text':'ALICE_ONLY'}).status_code==200
    assert b.post('/asrobot/api/chat',json={'text':'BOB_ONLY'}).status_code==200
    assert seen[0][0:2]==(ua,'key-for-alice') and seen[1][0:2]==(ub,'key-for-bob')
    assert 'ALICE_ONLY' not in seen[1][2]
    assert 'ALICE_ONLY' not in b.get('/asrobot/api/messages').text

def test_recurrence_deadlines_and_password_reset(clients):
    a,b=clients
    current=datetime.now(rt.SH)
    item={'at':(current-timedelta(days=2,seconds=5)).isoformat(),'repeat':'daily','due_at':(current+timedelta(days=1)).isoformat()}
    jobs=rt.schedule(item,'task',current)
    assert len(jobs)==3 and abs((jobs[0][1]-current).total_seconds())<10
    uid=b.get('/asrobot/api/me').json()['user']['id']
    temporary=a.post('/asrobot/api/admin/users/'+uid,json={'action':'reset'}).json()['temporary_password']
    r=b.post('/asrobot/api/login',json={'login':'bob','password':temporary})
    assert r.status_code==200 and r.json()['user']['must_change']==1
    b.headers['X-CSRF-Token']=r.json()['csrf']
    assert b.get('/asrobot/api/settings').status_code==403
    assert b.post('/asrobot/api/password',json={'current':temporary,'password':'new-password-123456'}).status_code==200
    assert b.get('/asrobot/api/me').status_code==401

def test_slow_model_does_not_block_native_reminder(clients,monkeypatch):
    a,_=clients; uid=a.get('/asrobot/api/me').json()['user']['id']
    a.put('/asrobot/api/settings',json={'revision':0,'routine_start':'00:00','routine_end':'23:59'})
    a.post('/asrobot/api/items/task',json={'title':'native','at':(datetime.now(rt.SH)-timedelta(seconds=1)).isoformat()})
    calls=[]
    async def scenario():
        started=asyncio.Event(); release=asyncio.Event()
        async def slow_model(*args,**kwargs):
            started.set(); await release.wait(); return {'role':'assistant','content':'done'}
        async def send(uid,text): calls.append(text)
        monkeypatch.setattr(rt,'model',slow_model); monkeypatch.setattr(rt,'send',send)
        task=asyncio.create_task(rt.chat(uid,'hello'))
        await started.wait()
        try: await asyncio.wait_for(rt.tick(),timeout=1)
        finally: release.set(); await task
    asyncio.run(scenario()); assert len(calls)==1

def test_unbind_during_batch_does_not_restore_old_credentials(clients,monkeypatch):
    a,_=clients; uid=a.get('/asrobot/api/me').json()['user']['id']
    s.put_secret(uid,'wechat',{'base':rt.WX,'token':'old-token','user':'wx-user','cursor':'','context':'ctx'})
    with s.db() as c: c.execute('INSERT INTO bindings(user_id,external_id,status) VALUES(?,?,?)',(uid,'bot','bound'))
    async def fake_request(*args,**kwargs):
        return {'get_updates_buf':'next','msgs':[{'message_id':str(i),'from_user_id':'wx-user','message_type':1,'context_token':'ctx','item_list':[{'type':1,'text_item':{'text':'hello'}}]} for i in range(2)]}
    async def fake_chat(*args):
        with s.db() as c:
            c.execute('DELETE FROM bindings WHERE user_id=?',(uid,)); c.execute("DELETE FROM secrets WHERE user_id=? AND kind='wechat'",(uid,))
        return 'reply'
    async def fake_send(*args): pass
    monkeypatch.setattr(rt,'wx_request',fake_request); monkeypatch.setattr(rt,'chat',fake_chat); monkeypatch.setattr(rt,'send',fake_send)
    asyncio.run(asyncio.wait_for(rt.poll_user(uid),timeout=1))
    assert s.secret(uid,'wechat') is None

def test_weekly_and_date_override_routine(clients):
    a,_=clients
    weekly={day:{'enabled':True,'start':'09:48','end':'23:30'} for day in rt.DAYS}
    weekly['friday']['start']='07:58'
    assert a.put('/asrobot/api/settings',json={'revision':0,'weekly_schedule':weekly,'date_overrides':{'2026-10-30':{'enabled':False,'start':'07:58','end':'23:30'}}}).status_code==200
    cfg=a.get('/asrobot/api/settings').json()
    assert rt.admitted(cfg,datetime.fromisoformat('2026-11-06T08:00:00+08:00'))
    assert not rt.admitted(cfg,datetime.fromisoformat('2026-11-05T08:00:00+08:00'))
    assert not rt.admitted(cfg,datetime.fromisoformat('2026-10-30T12:00:00+08:00'))
    assert a.put('/asrobot/api/settings',json={'revision':1,'weekly_schedule':{'monday':weekly['monday']}}).status_code==400

def test_maas_request_effort_and_fixed_endpoint(clients,monkeypatch):
    import httpx
    a,_=clients
    a.put('/asrobot/api/settings',json={'revision':0,'provider':'qianwen_maas','model':'qwen3.8-flash','reasoning_effort':'medium','api_key':'only-my-key'})
    uid=a.get('/asrobot/api/me').json()['user']['id']; captured=[]
    def handler(req):
        captured.append((str(req.url),json.loads(req.content),req.headers['authorization']))
        return httpx.Response(200,json={'choices':[{'message':{'role':'assistant','content':'ok'}}]})
    original=httpx.AsyncClient
    monkeypatch.setattr(rt.httpx,'AsyncClient',lambda **kw:original(**kw,transport=httpx.MockTransport(handler)))
    for effort in ('low','medium','xhigh'):
        cfg=a.get('/asrobot/api/settings').json();a.put('/asrobot/api/settings',json={'revision':cfg['revision'],'reasoning_effort':effort})
        asyncio.run(rt.model(uid,[{'role':'user','content':'test'}]))
        assert captured[-1][0]=='https://maas.qianwenaiapi.com/compatible-mode/v1/chat/completions'
        assert captured[-1][1]['reasoning_effort']==effort
        assert captured[-1][2]=='Bearer only-my-key'

def test_migration_is_atomic_scoped_and_idempotent(clients, monkeypatch):
    from app.migrate import apply
    a,b=clients
    monkeypatch.setenv('ASROBOT_MIGRATION_LOGIN', 'example-owner')
    payload={'login':'example-owner','password':'TestPass123','model_key':'migration-key','source':{'name':'示例助手','address':'同学','model':{'base_url':rt.PROVIDERS['qianwen_maas'][1],'model':'qwen3.8-flash','effort':'medium'},'prompt':'# Friday\n你叫Friday。\n## 记忆与执行\nassistant_tasks old protocol\n## 可靠性与边界\n如实说明能力。','weekly_schedule':{d:{'enabled':True,'start':'09:48','end':'23:30'} for d in rt.DAYS},'date_overrides':{},'fingerprint':{'config':'fake-source-hash'},'enabled_modules':['mail_check']}}
    preview=apply(payload)
    assert preview['status']=='preview'
    with s.db() as c: assert not c.execute('SELECT 1 FROM users WHERE login=?', (payload['login'],)).fetchone()
    result=apply(payload,True); assert result['status']=='applied'
    with s.db() as c:
        row=c.execute('SELECT * FROM users WHERE login=?', (payload['login'],)).fetchone()
        assert row['password'].startswith('$argon2id$') and ph.verify(row['password'],payload['password'])
        cfg=s.settings(row['id']); assert cfg['address']=='同学' and cfg['name']=='示例助手' and 'assistant_tasks' not in cfg['prompt']
        assert c.execute('SELECT count(*) FROM entities WHERE user_id=?',(row['id'],)).fetchone()[0]==0
        assert c.execute('SELECT count(*) FROM bindings WHERE user_id=?',(row['id'],)).fetchone()[0]==0
        assert s.secret(row['id'],'model')=='migration-key'
    assert apply(payload,True)['status']=='already-applied'
    assert b.get('/asrobot/api/settings').json()['name']=='我的助手'
    assert 'migration-key' not in (s.DATA/'platform.sqlite3').read_bytes().decode(errors='ignore')

def test_migration_rejects_existing_account(clients):
    from app.migrate import apply
    with pytest.raises(ValueError): apply({'login':'another'})

def test_passwords_have_no_length_gate_or_silent_truncation(clients):
    a,_=clients
    invite=a.post('/asrobot/api/admin/invites',json={}).json()['invite']
    long_password='a'*160+'tail'
    with TestClient(app,base_url=ORIGIN) as client:
        client.headers['Origin']=ORIGIN
        data={'login':'length-test','password':'','invite':invite,'agree':True}
        assert client.post('/asrobot/api/register',json=data).status_code==400
        data['password']=long_password
        assert client.post('/asrobot/api/register',json=data).status_code==200
        assert client.post('/asrobot/api/login',json={'login':'length-test','password':'a'*160+'wrong'}).status_code==401
        result=client.post('/asrobot/api/login',json={'login':'length-test','password':long_password})
        assert result.status_code==200
        client.headers['X-CSRF-Token']=result.json()['csrf']
        assert client.post('/asrobot/api/password',json={'current':long_password,'password':'x'}).status_code==200
        assert client.post('/asrobot/api/login',json={'login':'length-test','password':'x'}).status_code==200
