import asyncio,io,json,zipfile
import httpx,pytest
from test_platform import clients
from app import store as s,runtime as rt,native

@pytest.fixture
def original(clients,tmp_path,monkeypatch):
    admin,owner=clients
    uid=owner.get('/asrobot/api/me').json()['user']['id'];s.workspace(uid)
    with s.db() as c:c.execute("UPDATE workspaces SET native_source='friday_full' WHERE user_id=?",(uid,))
    token=tmp_path/'native-token';token.write_text('private-bridge-credential');monkeypatch.setenv('ASROBOT_NATIVE_TOKEN',str(token))
    calls=[];failure={'on':False}
    def handle(request):
        assert request.headers['authorization']=='Bearer private-bridge-credential'
        assert request.url.params['source']=='friday_full'
        payload=json.loads(request.content or '{}');calls.append((request.url.path,payload))
        if failure['on']:return httpx.Response(503,json={'error':'原实例暂时不可达'})
        if request.url.path=='/v1/status':result={'running':True,'wechat_configured':True,'model_ref':'original/model','counts':{'todo':9},'mail_accounts':[{'id':'qq','credential_configured':True}]}
        elif request.url.path=='/v1/profile':result={'prompt':'完整原提示词'*2000,'revision':'opaque','model_ref':'original/model','providers':[]}
        elif request.url.path=='/v1/tool':result=[{'id':'native-record','title':'ORIGINAL_ONLY','revision':4}]
        elif request.url.path=='/v1/chat':result={'text':'原 Friday 的答复','engine':'original_friday'}
        else:result={'ok':True}
        return httpx.Response(200,json=result)
    monkeypatch.setattr(native.httpx,'AsyncHTTPTransport',lambda **kwargs:httpx.MockTransport(handle))
    return admin,owner,uid,calls,failure

def test_original_runtime_owner_scoping_and_role(original):
    admin,owner,uid,calls,_=original
    assert owner.get('/asrobot/api/settings').json()['native_mode']
    assert owner.get('/asrobot/api/workspace').json()['native_mode']
    assert owner.get('/asrobot/api/native/status').json()['counts']['todo']==9
    assert admin.get('/asrobot/api/native/status?user_id='+uid+'&source=friday_full').status_code==404
    assert admin.post('/asrobot/api/native/tool',json={'tool':'assistant_mail','args':{'action':'status'},'user_id':uid}).status_code==404
    assert len(calls)==2
    assert owner.get('/asrobot/api/admin/users').status_code==403

def test_web_chat_uses_full_engine_without_restricted_fallback(original,monkeypatch):
    _,owner,uid,calls,_=original
    async def forbidden(*args,**kwargs):raise AssertionError('restricted assistant used')
    monkeypatch.setattr(rt,'model',forbidden)
    assert owner.post('/asrobot/api/chat',json={'text':'验证完整原配置'}).json()['text']=='原 Friday 的答复'
    assert calls==[('/v1/chat',{'text':'验证完整原配置'})]
    with s.db() as c:
        rows=c.execute('SELECT role,text FROM messages WHERE user_id=? ORDER BY id',(uid,)).fetchall()
    assert [r['role'] for r in rows]==['user','assistant']

def test_original_settings_are_not_written_to_shadow_copy(original):
    _,owner,uid,calls,_=original
    before=s.settings(uid)
    assert owner.put('/asrobot/api/settings',json={'revision':0,'name':'wrong engine'}).status_code==409
    assert s.settings(uid)==before and calls==[]
    assert owner.post('/asrobot/api/items/task',json={'title':'shadow task'}).status_code==409
    assert owner.get('/asrobot/api/items/task').status_code==409
    assert owner.post('/asrobot/api/wechat/pairings',json={}).status_code==409
    assert owner.delete('/asrobot/api/wechat').status_code==409

def test_original_reminders_have_no_second_platform_scheduler(original,monkeypatch):
    _,_,uid,_,_=original
    s.settings(uid)
    async def forbidden(*args,**kwargs):raise AssertionError('second scheduler used')
    monkeypatch.setattr(rt,'send',forbidden)
    asyncio.run(rt.tick_user(uid))

def test_original_wechat_status_and_explicit_test_job(original):
    _,owner,uid,calls,_=original
    assert owner.get('/asrobot/api/wechat').json()['status']=='native_running'
    r=owner.post('/asrobot/api/wechat/test',json={})
    assert r.status_code==200
    _,body=calls[-1];assert body['tool']=='assistant_tasks' and body['args']['kind']=='reminder'
    assert 'chat_id' not in body['args'] and 'target' not in body['args']

def test_disable_native_owner_stops_original_before_revoking_access(original):
    admin,owner,uid,calls,_=original
    assert admin.post('/asrobot/api/admin/users/'+uid,json={'action':'disable'}).status_code==200
    assert calls[-1][0]=='/v1/pause'
    assert owner.get('/asrobot/api/me').status_code==401
    assert admin.post('/asrobot/api/admin/users/'+uid,json={'action':'enable'}).status_code==200
    assert calls[-1][0]=='/v1/resume'

def test_unavailable_native_cannot_silently_succeed_lifecycle(original):
    admin,owner,uid,calls,failure=original;failure['on']=True
    assert admin.post('/asrobot/api/admin/users/'+uid,json={'action':'disable'}).status_code==503
    assert s.active(uid)
    assert owner.get('/asrobot/api/native/status').status_code==503

def test_native_export_contains_full_prompt_and_original_records(original):
    _,owner,uid,calls,_=original
    r=owner.post('/asrobot/api/export',json={});assert r.status_code==200
    with zipfile.ZipFile(io.BytesIO(r.content)) as z:
        data=json.loads(z.read('asrobot.json'))
        assert data['native']['tasks'][0]['title']=='ORIGINAL_ONLY'
        assert len(data['native']['profile']['prompt'])>6000
        assert 'private-bridge-credential' not in json.dumps(data)

def test_account_delete_stops_original_and_removes_mapping(original):
    _,owner,uid,calls,_=original
    r=owner.request('DELETE','/asrobot/api/account',json={'confirm':'bob','password':'test-password-123'})
    assert r.status_code==200 and calls[-1][0]=='/v1/pause'
    assert not s.native(uid)

def test_native_writes_reject_csrf_and_arbitrary_routes(original):
    _,owner,uid,calls,_=original
    assert owner.post('/asrobot/api/native/tool',json={},headers={'X-CSRF-Token':'wrong'}).status_code==403
    assert owner.post('/asrobot/api/native/pause',json={}).status_code==404
    assert owner.post('/asrobot/api/native/anything',json={}).status_code==404
    assert calls==[]
