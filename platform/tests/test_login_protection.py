import asyncio
import pytest
from fastapi.testclient import TestClient
from app import store as s, runtime as rt, login_protection as lp
from app.main import app, ph

ORIGIN='http://localhost:18980'
@pytest.fixture
def browser(tmp_path, monkeypatch):
    monkeypatch.setattr(s,'DATA',tmp_path)
    monkeypatch.delenv('ASROBOT_KEY_FILE',raising=False)
    monkeypatch.delenv('ASROBOT_REDIS_URL',raising=False)
    monkeypatch.setattr(lp.secrets,'choice',lambda _: '2')
    async def idle(): await asyncio.sleep(3600)
    monkeypatch.setattr(rt,'supervisor',idle)
    with TestClient(app,base_url=ORIGIN,headers={'Origin':ORIGIN}) as c:
        with s.db() as db: db.execute('INSERT INTO users(id,login,password,role,created) VALUES(?,?,?,?,?)',(s.ident(),'alice',ph.hash('test-password-123'),'user',s.now()))
        yield c

def challenge(c):
    r=c.get('/asrobot/api/login/captcha'); assert r.status_code==200
    assert r.json()['image'].startswith('data:image/png;base64,')
    return r.json()['key']

def test_captcha_one_time_and_session_lifetime(browser,monkeypatch):
    key=challenge(browser)
    payload={'login':'alice','password':'test-password-123','captcha_key':key,'captcha_code':'2222'}
    r=browser.post('/asrobot/api/login',json=payload); assert r.status_code==200,r.text
    assert 'Max-Age=10800' in r.headers['set-cookie']
    with s.db() as db: expires=db.execute('SELECT expires FROM sessions').fetchone()[0]
    assert 10795 <= expires-s.now() <=10800
    assert browser.post('/asrobot/api/login',json=payload).status_code==400
    monkeypatch.setattr(s,'now',lambda: expires)
    assert browser.get('/asrobot/api/me').status_code==401

def test_incorrect_captcha_consumed(browser):
    key=challenge(browser)
    payload={'login':'alice','password':'test-password-123','captcha_key':key,'captcha_code':'9999'}
    assert browser.post('/asrobot/api/login',json=payload).status_code==400
    payload['captcha_code']='2222'
    assert browser.post('/asrobot/api/login',json=payload).status_code==400

def test_password_failures_lock_account(browser):
    for _ in range(5):
        r=browser.post('/asrobot/api/login',json={'login':'alice','password':'wrong','captcha_key':challenge(browser),'captcha_code':'2222'})
        assert r.status_code==401
    assert browser.post('/asrobot/api/login',json={'login':'alice','password':'test-password-123','captcha_key':challenge(browser),'captcha_code':'2222'}).status_code==429

def test_expired_challenge_and_redis_unavailable(browser,monkeypatch):
    monkeypatch.setenv('ASROBOT_REDIS_URL','redis://127.0.0.1:1/0')
    key=challenge(browser)
    with s.db() as db: db.execute('UPDATE login_captchas SET expires=?',(s.now()-1,))
    assert browser.post('/asrobot/api/login',json={'login':'alice','password':'test-password-123','captcha_key':key,'captcha_code':'2222'}).status_code==400
    key=challenge(browser)
    assert browser.post('/asrobot/api/login',json={'login':'alice','password':'test-password-123','captcha_key':key,'captcha_code':'2222'}).status_code==200

def test_redis_challenge_and_failure_ttl(browser,monkeypatch,tmp_path):
    import shutil, subprocess, time
    server=shutil.which('redis-server')
    if not server: pytest.skip('redis-server is not installed')
    import tempfile
    socket_path=tempfile.mktemp(prefix='asrobot-test-',suffix='.sock',dir='/tmp')
    proc=subprocess.Popen([server,'--port','0','--unixsocket',socket_path,'--unixsocketperm','700','--save','','--appendonly','no'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    try:
        from pathlib import Path
        for _ in range(100):
            if Path(socket_path).exists(): break
            if proc.poll() is not None: pytest.skip('isolated Redis could not start')
            time.sleep(.01)
        monkeypatch.setenv('ASROBOT_REDIS_URL','unix://'+socket_path+'?db=0')
        key=challenge(browser)
        r=lp.client(); cache_key='asrobot:captcha:'+s.digest(key)
        assert 0<r.ttl(cache_key)<=lp.CAPTCHA_TTL
        assert browser.post('/asrobot/api/login',json={'login':'alice','password':'wrong','captcha_key':key,'captcha_code':'2222'}).status_code==401
        assert not r.exists(cache_key)
        failure_key='asrobot:login-fail:'+s.digest('alice')
        assert r.get(failure_key)=='1' and 0<r.ttl(failure_key)<=lp.FAIL_TTL
        r.close()
    finally:
        proc.terminate(); proc.wait(timeout=5)
