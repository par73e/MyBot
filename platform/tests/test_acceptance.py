"""End-to-end business acceptance with disposable tenants and fake external APIs."""
import asyncio, io, json, zipfile
from datetime import datetime, timedelta
import pytest
from fastapi.testclient import TestClient
from test_platform import clients, ORIGIN
from app.main import app, ph
from app import store as s, runtime as rt

@pytest.mark.parametrize('path',['/me','/settings','/items/task','/items/memory','/items/expiry','/messages','/activity','/wechat','/admin/users'])
def test_private_reads_require_login(clients,path):
    with TestClient(app,base_url=ORIGIN) as c: assert c.get('/asrobot/api'+path).status_code==401

@pytest.mark.parametrize('payload',[{'login':[],'password':'x','agree':True},{'login':'xuser','password':None,'agree':True}])
def test_registration_invalid_types_are_client_errors(clients,payload):
    a,_=clients
    invite=a.post('/asrobot/api/admin/invites',json={}).json()['invite']
    with TestClient(app,base_url=ORIGIN) as c:
        r=c.post('/asrobot/api/register',json={**payload,'invite':invite},headers={'Origin':ORIGIN})
        assert r.status_code==400

@pytest.mark.parametrize('bad',[{'routine_start':None},{'routine_end':[]},{'name':{}},{'provider':'http://127.0.0.1'},{'modules':{'memory':'yes'}},{'weekly_schedule':[]},{'api_key':'   '}])
def test_invalid_settings_preserve_previous_configuration(clients,bad):
    a,_=clients
    before=a.get('/asrobot/api/settings').json()
    r=a.put('/asrobot/api/settings',json={'revision':before['revision'],**bad})
    assert r.status_code==400,r.text
    assert a.get('/asrobot/api/settings').json()['revision']==before['revision']

def test_entire_settings_roundtrip_and_provider_switch(clients):
    a,_=clients
    body={'revision':0,'name':'验收助手','address':'测试者','style':'清晰','prompt':'按实际结果回答','provider':'qianwen_maas','model':'qwen3.8-flash','reasoning_effort':'xhigh','api_key':'mock-key','routine_start':'07:00','routine_end':'23:00','modules':{'memory':False,'tasks':True,'expiry':False,'reminders':True},'weekly_schedule':{d:{'enabled':d!='sunday','start':'08:00','end':'22:00'} for d in rt.DAYS},'date_overrides':{'2030-01-01':{'enabled':False,'start':'08:00','end':'22:00'}}}
    assert a.put('/asrobot/api/settings',json=body).status_code==200
    got=a.get('/asrobot/api/settings').json()
    for k,v in body.items():
        if k not in ('revision','api_key'): assert got[k]==v
    assert got['key_configured'] and 'mock-key' not in json.dumps(got)
    assert a.put('/asrobot/api/settings',json={'revision':1,'provider':'deepseek'}).status_code==200
    assert not a.get('/asrobot/api/settings').json()['key_configured']

def test_model_key_replacement_and_explicit_deletion(clients):
    a,_=clients; uid=a.get('/asrobot/api/me').json()['user']['id']
    assert a.put('/asrobot/api/settings',json={'revision':0,'provider':'openai','model':'mock','api_key':'first-private-key'}).status_code==200
    assert s.secret(uid,'model')=='first-private-key'
    assert a.put('/asrobot/api/settings',json={'revision':1,'api_key':'replacement-private-key'}).status_code==200
    assert s.secret(uid,'model')=='replacement-private-key'
    assert a.put('/asrobot/api/settings',json={'revision':2,'delete_key':True}).status_code==200
    assert s.secret(uid,'model') is None
    assert not a.get('/asrobot/api/settings').json()['key_configured']
    assert a.post('/asrobot/api/model/test',json={}).status_code==400

@pytest.mark.parametrize('kind', ['task','memory','expiry'])
def test_business_record_full_lifecycle(clients,kind):
    a,b=clients
    payload={'title':'验收'+kind,'body':'正文 <script>alert(1)</script>','expires_at':'2030-01-08T12:00:00+08:00'}
    result=a.post('/asrobot/api/items/'+kind,json=payload)
    assert result.status_code==200,result.text
    eid=result.json()['id']; record=a.get('/asrobot/api/items/'+kind).json()[0]
    if kind=='expiry': assert record['remind_at']=='2030-01-01T12:00:00+08:00'
    assert b.get('/asrobot/api/items/'+kind).json()==[]
    assert a.post('/asrobot/api/items/'+kind,json={**record,'title':'updated','status':'completed'}).status_code==200
    current=a.get('/asrobot/api/items/'+kind).json()[0]
    assert current['title']=='updated' and current['status']=='completed'
    assert a.request('DELETE','/asrobot/api/items/'+kind+'/'+eid,json={'revision':current['revision']}).status_code==200
    assert a.get('/asrobot/api/items/'+kind).json()==[]

@pytest.mark.parametrize('kind', ['task','memory','expiry'])
def test_disabled_business_modules_reject_writes(clients,kind):
    a,_=clients; module={'task':'tasks','memory':'memory','expiry':'expiry'}[kind]
    a.put('/asrobot/api/settings',json={'revision':0,'modules':{module:False}})
    r=a.post('/asrobot/api/items/'+kind,json={'title':'blocked','expires_at':'2030-01-01T12:00:00+08:00'})
    assert r.status_code==400

def test_password_change_revokes_every_session(clients):
    a,_=clients
    with TestClient(app,base_url=ORIGIN) as other:
        r=other.post('/asrobot/api/login',json={'login':'alice','password':'test-password-123'},headers={'Origin':ORIGIN})
        cookie=r.headers['set-cookie'].lower(); assert 'httponly' in cookie and 'samesite=strict' in cookie and 'path=/asrobot/' in cookie
        assert a.post('/asrobot/api/password',json={'current':'wrong','password':'x'}).status_code==400
        assert a.post('/asrobot/api/password',json={'current':'test-password-123','password':'x'}).status_code==200
        assert a.get('/asrobot/api/me').status_code==401 and other.get('/asrobot/api/me').status_code==401

def test_history_pagination_day_boundary_and_isolation(clients):
    a,b=clients; uid=a.get('/asrobot/api/me').json()['user']['id']; other=b.get('/asrobot/api/me').json()['user']['id']
    start=int(datetime.fromisoformat('2030-01-01T06:00:00+08:00').timestamp())
    with s.db() as c:
        for i in range(230): c.execute('INSERT INTO messages(user_id,role,text,source,created) VALUES(?,?,?,?,?)',(uid,'user','message-'+str(i),'web',start+i))
        c.execute('INSERT INTO messages(user_id,role,text,source,created) VALUES(?,?,?,?,?)',(uid,'user','previous-day','web',start-1))
        c.execute('INSERT INTO messages(user_id,role,text,source,created) VALUES(?,?,?,?,?)',(other,'user','foreign-private','web',start))
    seen=[]; before=0
    for _ in range(3):
        result=a.get('/asrobot/api/messages',params={'date':'2030-01-01','before':before}).json()
        seen.extend(m['id'] for m in result['messages']); before=result['next_before']
    assert len(seen)==len(set(seen))==230
    assert not result['has_more']
    assert 'foreign-private' not in a.get('/asrobot/api/messages?date=2030-01-01').text
    assert 'previous-day' in a.get('/asrobot/api/messages?date=2029-12-31').text

def test_export_contains_all_owned_content_but_no_credentials(clients):
    a,b=clients; uid=a.get('/asrobot/api/me').json()['user']['id']
    for kind in ('task','memory','expiry'): a.post('/asrobot/api/items/'+kind,json={'title':'owned-'+kind,'expires_at':'2030-01-08T12:00:00+08:00'})
    b.post('/asrobot/api/items/task',json={'title':'foreign-private'})
    a.put('/asrobot/api/settings',json={'revision':0,'provider':'openai','model':'mock','api_key':'never-export-key'})
    s.put_secret(uid,'wechat',{'token':'never-export-wx','context':'never-export-context'})
    r=a.post('/asrobot/api/export',json={}); assert r.status_code==200
    with zipfile.ZipFile(io.BytesIO(r.content)) as z:
        exported=json.loads(z.read('asrobot.json')); raw=json.dumps(exported)
        assert len(exported['items'])==3 and exported['manifest']['version']==1
        for private in ('never-export-key','never-export-wx','never-export-context','foreign-private','test-password-123'): assert private not in raw

def test_account_last_admin_and_wrong_confirmation(clients):
    a,b=clients
    assert a.request('DELETE','/asrobot/api/account',json={'confirm':'alice','password':'test-password-123'}).status_code==400
    assert b.request('DELETE','/asrobot/api/account',json={'confirm':'wrong','password':'test-password-123'}).status_code==400
    assert b.get('/asrobot/api/me').status_code==200
    assert b.post('/asrobot/api/admin/invites',json={}).status_code==403

def test_chat_tool_roundtrip_is_host_scoped(clients,monkeypatch):
    a,b=clients; calls=[]
    async def fake(uid,messages,tools=False):
        calls.append(messages)
        if messages[-1]['role']=='tool': return {'role':'assistant','content':'已保存验收记忆'}
        return {'role':'assistant','content':None,'tool_calls':[{'id':'tool-1','type':'function','function':{'name':'save_item','arguments':json.dumps({'kind':'memory','title':'验收偏好','body':'喜欢简洁'})}}]}
    monkeypatch.setattr(rt,'model',fake)
    r=a.post('/asrobot/api/chat',json={'text':'记住验收偏好'})
    assert r.status_code==200 and len(calls)==2
    assert a.get('/asrobot/api/items/memory').json()[0]['body']=='喜欢简洁'
    assert b.get('/asrobot/api/items/memory').json()==[]
    assert '已保存验收记忆' in a.get('/asrobot/api/messages').text

def test_completed_and_cancelled_reminders_do_not_send(clients,monkeypatch):
    a,_=clients; calls=[]
    async def fake(*args): calls.append(args)
    monkeypatch.setattr(rt,'send',fake)
    for state in ('completed','cancelled'):
        a.post('/asrobot/api/items/task',json={'title':state,'status':state,'at':(datetime.now(rt.SH)-timedelta(seconds=1)).isoformat()})
    asyncio.run(rt.tick()); assert not calls

def test_wechat_login_states_verification_and_secret_filtering(clients,monkeypatch):
    a,b=clients; states=iter(['wait','scaned','need_verifycode','confirmed']); seen=[]
    async def fake(base,path,**kwargs):
        seen.append(kwargs)
        if path.endswith('get_bot_qrcode'): return {'qrcode':'fake-qr','qrcode_img_content':'https://example.test/fake-qr'}
        state=next(states)
        return {'status':state,'ilink_bot_id':'fake-bot','bot_token':'never-leak','ilink_user_id':'fake-user','baseurl':rt.WX}
    monkeypatch.setattr(rt,'wx_request',fake)
    qr=a.post('/asrobot/api/wechat/pairings',json={}).json(); assert qr['image'].startswith('data:image/png;base64,')
    for expected in ('wait','scaned','need_verifycode','confirmed'):
        r=a.post('/asrobot/api/wechat/pairings/'+qr['id'],json={'verify_code':'123456'} if expected=='confirmed' else {})
        assert r.status_code==200 and r.json()['status']==expected
    assert seen[-1]['params']['verify_code']=='123456'
    assert 'never-leak' not in r.text+a.get('/asrobot/api/wechat').text
    assert b.get('/asrobot/api/wechat').json()['status']=='unbound'
    a.delete('/asrobot/api/wechat'); assert a.get('/asrobot/api/wechat').json()['status']=='unbound'

@pytest.mark.parametrize('status',['expired','verify_code_blocked','binded_redirect'])
def test_wechat_terminal_login_states_do_not_bind(clients,monkeypatch,status):
    a,_=clients
    async def fake(base,path,**kw):
        return {'qrcode':'fake-qr'} if path.endswith('get_bot_qrcode') else {'status':status}
    monkeypatch.setattr(rt,'wx_request',fake)
    pid=a.post('/asrobot/api/wechat/pairings',json={}).json()['id']
    assert a.post('/asrobot/api/wechat/pairings/'+pid,json={}).json()['status']==status
    assert a.get('/asrobot/api/wechat').json()['status']=='unbound'

def test_wechat_expired_attempt_and_redirect_ssrf(clients,monkeypatch):
    a,_=clients
    async def fake(base,path,**kw):
        return {'qrcode':'fake-qr'} if path.endswith('get_bot_qrcode') else {'status':'scaned_but_redirect','redirect_host':'127.0.0.1'}
    monkeypatch.setattr(rt,'wx_request',fake)
    pid=a.post('/asrobot/api/wechat/pairings',json={}).json()['id']
    assert a.post('/asrobot/api/wechat/pairings/'+pid,json={}).status_code==400
    with s.db() as c: c.execute('UPDATE pairings SET expires=0 WHERE id=?',(pid,))
    assert a.post('/asrobot/api/wechat/pairings/'+pid,json={}).status_code==400

def test_repeated_pairing_confirmation_does_not_reset_live_context(clients,monkeypatch):
    a,_=clients
    async def fake(base,path,**kw):
        if path.endswith('get_bot_qrcode'): return {'qrcode':'fake-qr'}
        return {'status':'confirmed','ilink_bot_id':'bot','ilink_user_id':'wx','bot_token':'token','baseurl':rt.WX}
    monkeypatch.setattr(rt,'wx_request',fake)
    pid=a.post('/asrobot/api/wechat/pairings',json={}).json()['id']
    assert a.post('/asrobot/api/wechat/pairings/'+pid,json={}).status_code==200
    uid=a.get('/asrobot/api/me').json()['user']['id']; credential=s.secret(uid,'wechat'); credential['context']='active-context'; s.put_secret(uid,'wechat',credential)
    assert a.post('/asrobot/api/wechat/pairings/'+pid,json={}).status_code==200
    assert s.secret(uid,'wechat')['context']=='active-context'

def test_wechat_receive_reply_sender_filter_and_dedup(clients,monkeypatch):
    a,b=clients; uid=a.get('/asrobot/api/me').json()['user']['id']; requests=[]; count=0
    s.put_secret(uid,'wechat',{'token':'private-token','base':rt.WX,'user':'owner','context':'','cursor':''})
    with s.db() as c: c.execute('INSERT INTO bindings(user_id,external_id) VALUES(?,?)',(uid,'bot'))
    def message(mid,user): return {'message_id':mid,'from_user_id':user,'message_type':1,'context_token':'ctx','item_list':[{'type':1,'text_item':{'text':'验收消息'}}]}
    async def fake(base,path,token=None,body=None,**kw):
        nonlocal count
        requests.append((path,token,body))
        if path.endswith('getupdates'):
            count+=1
            if count>1: raise asyncio.CancelledError()
            return {'get_updates_buf':'next','msgs':[message('1','owner'),message('1','owner'),message('2','foreign')]}
        return {'ret':0}
    async def model(*args,**kw): return {'role':'assistant','content':'验收回复'}
    monkeypatch.setattr(rt,'wx_request',fake); monkeypatch.setattr(rt,'model',model)
    with pytest.raises(asyncio.CancelledError): asyncio.run(rt.poll_user(uid))
    outbound=[body for path,token,body in requests if path.endswith('sendmessage')]
    assert len(outbound)==1 and outbound[0]['msg']['to_user_id']=='owner' and outbound[0]['msg']['context_token']=='ctx'
    assert '验收回复' in a.get('/asrobot/api/messages').text
    assert '验收回复' not in b.get('/asrobot/api/messages').text

def test_wechat_missing_context_is_reported_as_not_sent(clients):
    a,_=clients; uid=a.get('/asrobot/api/me').json()['user']['id']
    s.put_secret(uid,'wechat',{'token':'fake','base':rt.WX,'user':'owner','context':''})
    a.put('/asrobot/api/settings',json={'revision':0,'routine_start':'00:00','routine_end':'23:59'})
    a.post('/asrobot/api/items/task',json={'title':'precondition','at':(datetime.now(rt.SH)-timedelta(seconds=1)).isoformat()})
    asyncio.run(rt.tick())
    assert a.get('/asrobot/api/items/task').json()[0]['notification_state']=='not_sent'

@pytest.mark.parametrize('provider',list(rt.PROVIDERS))
def test_all_model_provider_endpoints_are_fixed_and_private(clients,monkeypatch,provider):
    import httpx
    a,_=clients; seen=[]
    a.put('/asrobot/api/settings',json={'revision':0,'provider':provider,'model':'mock','api_key':'tenant-key'})
    uid=a.get('/asrobot/api/me').json()['user']['id']
    def fake(req):
        seen.append(req)
        return httpx.Response(200,json={'choices':[{'message':{'role':'assistant','content':'ok'}}]})
    original=httpx.AsyncClient
    monkeypatch.setattr(rt.httpx,'AsyncClient',lambda **kw:original(**kw,transport=httpx.MockTransport(fake)))
    assert asyncio.run(rt.model(uid,[{'role':'user','content':'验收'}]))['content']=='ok'
    assert str(seen[0].url)==rt.PROVIDERS[provider][1]+'/chat/completions'
    assert seen[0].headers['authorization']=='Bearer tenant-key'

@pytest.mark.parametrize('status',[401,429,503])
def test_model_failure_messages_never_repeat_provider_secret(clients,monkeypatch,status):
    import httpx
    a,_=clients
    a.put('/asrobot/api/settings',json={'revision':0,'provider':'openai','model':'mock','api_key':'do-not-repeat-key'})
    original=httpx.AsyncClient
    monkeypatch.setattr(rt.httpx,'AsyncClient',lambda **kw:original(**kw,transport=httpx.MockTransport(lambda req:httpx.Response(status,json={'error':'do-not-repeat-key'}))))
    r=a.post('/asrobot/api/model/test',json={})
    assert r.status_code==400 and 'do-not-repeat-key' not in r.text

def test_concurrent_pairing_confirmation_preserves_already_activated_binding(clients,monkeypatch):
    a,_=clients; uid=a.get('/asrobot/api/me').json()['user']['id']; pid=None
    async def fake(base,path,**kw):
        if path.endswith('get_bot_qrcode'): return {'qrcode':'fake-qr'}
        # Another in-flight confirmation completed after this request read its attempt.
        with s.db() as c:
            c.execute('INSERT INTO bindings(user_id,external_id) VALUES(?,?)',(uid,'same-bot'))
            c.execute("UPDATE pairings SET status='confirmed',secret=? WHERE id=?",(s.encrypt({}),pid))
        s.put_secret(uid,'wechat',{'token':'token','base':rt.WX,'user':'wx','context':'already-active-context'})
        return {'status':'confirmed','ilink_bot_id':'same-bot','ilink_user_id':'wx','bot_token':'token','baseurl':rt.WX}
    monkeypatch.setattr(rt,'wx_request',fake)
    pid=a.post('/asrobot/api/wechat/pairings',json={}).json()['id']
    assert a.post('/asrobot/api/wechat/pairings/'+pid,json={}).status_code==200
    assert s.secret(uid,'wechat')['context']=='already-active-context'
