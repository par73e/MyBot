"""Adversarial checks use disposable tenants and no external services."""
import asyncio,threading,time
from types import SimpleNamespace
import httpx,pytest
from fastapi.testclient import TestClient
from test_platform import clients,ORIGIN
from app import main,store as s,runtime as rt

def test_login_account_throttle_survives_changing_ip(clients,monkeypatch):
    monkeypatch.setattr(main,'verify',lambda *args:False)
    async def attempt():
        results=[]
        for i in range(12):
            transport=httpx.ASGITransport(app=main.app,client=('192.0.2.'+str(i+1),1234))
            async with httpx.AsyncClient(transport=transport,base_url=ORIGIN) as c:
                results.append((await c.post('/asrobot/api/login',headers={'Origin':ORIGIN},json={'login':'bob','password':'wrong'})).status_code)
        return results
    assert asyncio.run(attempt())==[401]*10+[429]*2

def test_password_verification_has_bounded_concurrency(clients,monkeypatch):
    running=0;maximum=0;guard=threading.Lock()
    def expensive(*args):
        nonlocal running,maximum
        with guard:running+=1;maximum=max(maximum,running)
        time.sleep(.2)
        with guard:running-=1
        return False
    monkeypatch.setattr(main,'verify',expensive)
    async def attempt():
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app),base_url=ORIGIN) as c:
            return await asyncio.gather(*[c.post('/asrobot/api/login',headers={'Origin':ORIGIN},json={'login':'absent'+str(i),'password':'wrong'}) for i in range(12)])
    responses=asyncio.run(attempt())
    assert maximum<=2,maximum
    assert all(r.status_code in (401,503) for r in responses)

def test_invalid_invite_rejected_before_password_hash(clients,monkeypatch):
    calls=[];monkeypatch.setattr(main,'ph',SimpleNamespace(hash=lambda p:calls.append(p) or 'unused'))
    a,_=clients
    r=a.post('/asrobot/api/register',json={'login':'charlie','password':'x','agree':True,'invite':'invalid'})
    assert r.status_code==400 and calls==[]

def test_overlong_password_rejected_before_verification(clients,monkeypatch):
    calls=[];monkeypatch.setattr(main,'verify',lambda *args:calls.append(args) or False)
    a,_=clients
    assert a.post('/asrobot/api/login',json={'login':'bob','password':'x'*4097}).status_code in (400,401)
    assert calls==[]

def test_password_confirmation_attempts_are_limited(clients,monkeypatch):
    a,_=clients;monkeypatch.setattr(main,'verify',lambda *args:False)
    results=[a.post('/asrobot/api/password',json={'current':'wrong','password':'new'}).status_code for _ in range(9)]
    assert results==[400]*8+[429]

@pytest.mark.parametrize('change',[{'provider':[]},{'reasoning_effort':{}},{'modules':{'tasks':1}}])
def test_malformed_settings_are_rejected_without_mutation(clients,change):
    a,_=clients
    r=a.put('/asrobot/api/settings',json={'revision':0,**change})
    assert r.status_code==400
    assert a.get('/asrobot/api/settings').json()['revision']==0

def test_chunked_body_is_stopped_at_size_limit(clients):
    consumed=[]
    async def check():
        async def payload():
            for i in range(100):consumed.append(i);yield b'x'*1024
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app),base_url=ORIGIN) as c:
            return await c.post('/asrobot/api/login',headers={'Origin':ORIGIN},content=payload())
    response=asyncio.run(check())
    assert response.status_code==413 and len(consumed)<=65

def test_csrf_denial_keeps_security_headers(clients):
    a,_=clients
    r=a.post('/asrobot/api/logout',json={},headers={'Origin':'https://attacker.invalid'})
    assert r.status_code==403
    assert r.headers.get('cache-control')=='no-store'
    assert "frame-ancestors 'none'" in r.headers.get('content-security-policy','')

@pytest.mark.parametrize('path',['/asrobot/static/%2e%2e/main.py','/asrobot/static/%2e%2e/%2e%2e/store.py','/asrobot/static/.env','/asrobot/platform.sqlite3'])
def test_static_paths_cannot_expose_source_database_or_keys(clients,path):
    a,_=clients
    response=a.get(path)
    assert response.status_code==404
    assert response.headers['cache-control']=='no-store'

def test_queued_write_rechecks_session_after_disable(clients):
    _,b=clients;uid=b.get('/asrobot/api/me').json()['user']['id']
    token=b.cookies.get('asrobot_session');csrf=b.headers['X-CSRF-Token']
    async def attack():
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app),base_url=ORIGIN,headers={'Cookie':'asrobot_session='+token,'Origin':ORIGIN,'X-CSRF-Token':csrf}) as c:
            gate=rt.lock(uid);await gate.acquire()
            task=asyncio.create_task(c.post('/asrobot/api/items/task',json={'title':'queued before disable'}))
            await asyncio.sleep(.05)
            with s.db() as db:
                db.execute("UPDATE users SET status='disabled' WHERE id=?",(uid,));db.execute('DELETE FROM sessions WHERE user_id=?',(uid,))
            gate.release()
            return await task
    try:r=asyncio.run(attack())
    finally:rt.locks.clear()
    assert r.status_code==401
    assert s.entities(uid,'task')==[]

def test_cancelled_password_request_does_not_release_running_worker():
    from app.security import password_work
    from fastapi import HTTPException
    started=threading.Event();release=threading.Event()
    def hold():
        started.set();release.wait(2)
    async def check():
        first=asyncio.create_task(password_work(hold))
        await asyncio.to_thread(started.wait,1)
        second=asyncio.create_task(password_work(hold))
        await asyncio.sleep(.02)
        first.cancel()
        with pytest.raises(asyncio.CancelledError):await first
        try:
            with pytest.raises(HTTPException) as error:await password_work(lambda:True)
            assert error.value.status_code==503
        finally:release.set()
        await second
        await asyncio.sleep(.01)
        assert await password_work(lambda:True)
    asyncio.run(check())

def test_queued_chat_rechecks_revoked_session_before_model(clients,monkeypatch):
    _,b=clients;uid=b.get('/asrobot/api/me').json()['user']['id']
    token=b.cookies.get('asrobot_session');csrf=b.headers['X-CSRF-Token']
    async def forbidden(*args,**kwargs):raise AssertionError('model must not run')
    monkeypatch.setattr(rt,'model',forbidden)
    async def check():
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app),base_url=ORIGIN,headers={'Cookie':'asrobot_session='+token,'Origin':ORIGIN,'X-CSRF-Token':csrf}) as c:
            gate=rt.turn_lock(uid);await gate.acquire()
            task=asyncio.create_task(c.post('/asrobot/api/chat',json={'text':'queued before logout'}))
            await asyncio.sleep(.05)
            with s.db() as db:db.execute('DELETE FROM sessions WHERE user_id=?',(uid,))
            gate.release()
            return await task
    try:response=asyncio.run(check())
    finally:rt.turn_locks.clear()
    assert response.status_code==401
    with s.db() as db:assert db.execute('SELECT count(*) FROM messages WHERE user_id=?',(uid,)).fetchone()[0]==0

def test_login_cookie_flags_and_no_hash_in_responses(clients):
    a,_=clients
    response=a.post('/asrobot/api/login',json={'login':'alice','password':'test-password-123'})
    cookie=response.headers['set-cookie'].lower()
    assert 'httponly' in cookie and 'samesite=strict' in cookie and 'path=/asrobot/' in cookie
    assert 'password' not in response.json()['user']
    assert '$argon2' not in response.text

def test_isolated_host_cookie_cannot_fall_back_to_legacy_cookie(clients,monkeypatch):
    from app import main
    a,_=clients
    legacy=a.cookies.get('asrobot_session')
    monkeypatch.setattr(main,'ORIGIN','https://asrobot.example.test')
    monkeypatch.setattr(main,'HOST_COOKIE',True)
    monkeypatch.setattr(main,'COOKIE_NAME','__Host-asrobot_session')
    monkeypatch.setattr(main,'COOKIE_PATH','/')
    with TestClient(main.app,base_url='https://asrobot.example.test') as c:
        c.headers['Origin']='https://asrobot.example.test'
        c.cookies.set('asrobot_session',legacy)
        assert c.get('/asrobot/api/me').status_code==401
        r=c.post('/asrobot/api/login',json={'login':'alice','password':'test-password-123'})
        assert r.status_code==200
        cookie=r.headers['set-cookie']
        assert cookie.startswith('__Host-asrobot_session=') and 'Secure' in cookie and 'Path=/' in cookie and 'Domain=' not in cookie
        c.headers['X-CSRF-Token']=r.json()['csrf']
        assert c.get('/asrobot/api/me').status_code==200
        logout=c.post('/asrobot/api/logout',json={})
        assert logout.status_code==200 and '__Host-asrobot_session=' in logout.headers['set-cookie']

def test_invite_registration_creates_workspace_in_same_commit(clients):
    a,_=clients;invite=a.post('/asrobot/api/admin/invites',json={}).json()['invite']
    assert a.post('/asrobot/api/register',json={'login':'newuser','password':'x','agree':True,'invite':invite}).status_code==200
    with s.db() as db:
        row=db.execute("SELECT w.* FROM users u JOIN workspaces w ON w.user_id=u.id WHERE u.login='newuser'").fetchone()
    assert row and row['native_source'] is None and row['legacy_source'] is None

def test_password_change_rechecks_revocation_after_hash(clients,monkeypatch):
    _,b=clients;uid=b.get('/asrobot/api/me').json()['user']['id']
    token=b.cookies.get('asrobot_session');csrf=b.headers['X-CSRF-Token']
    with s.db() as db:before=db.execute('SELECT password FROM users WHERE id=?',(uid,)).fetchone()[0]
    entered=threading.Event();release=threading.Event()
    def hash_password(value):entered.set();release.wait(2);return 'must-not-be-saved'
    monkeypatch.setattr(main,'verify',lambda *args:True)
    monkeypatch.setattr(main,'ph',SimpleNamespace(hash=hash_password))
    async def check():
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app),base_url=ORIGIN,headers={'Cookie':'asrobot_session='+token,'Origin':ORIGIN,'X-CSRF-Token':csrf}) as client:
            task=asyncio.create_task(client.post('/asrobot/api/password',json={'current':'old','password':'new'}))
            await asyncio.to_thread(entered.wait,1)
            with s.db() as db:db.execute('DELETE FROM sessions WHERE user_id=?',(uid,))
            release.set()
            return await task
    response=asyncio.run(check())
    assert response.status_code==401
    with s.db() as db:assert db.execute('SELECT password FROM users WHERE id=?',(uid,)).fetchone()[0]==before
