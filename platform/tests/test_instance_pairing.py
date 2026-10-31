import pytest
from app import native, runtime as rt, store as s
from test_platform import clients


def test_instance_pairing_recovers_after_interrupted_install(clients, monkeypatch):
    first, second = clients
    uid = second.get('/asrobot/api/me').json()['user']['id']
    with s.db() as db:
        db.execute("INSERT INTO assistant_instances VALUES(?,?,?,'active')", ('a'*32, uid, s.now()))
    calls = {'poll':0, 'install':0}
    async def fake_wx(base,path,**kwargs):
        if path.endswith('get_bot_qrcode'): return {'qrcode':'synthetic-qr','qrcode_img_content':'https://example.test/qr'}
        calls['poll'] += 1
        return {'status':'confirmed','bot_token':'synthetic-secret','ilink_bot_id':'synthetic-bot','ilink_user_id':'synthetic-user','baseurl':rt.WX}
    async def fake_bridge(owner, endpoint, body=None):
        assert owner == uid
        if endpoint == 'status': return {'running':False}
        assert endpoint == 'pair-wechat'
        assert body['account'] == 'synthetic-bot' and body['token'] == 'synthetic-secret'
        calls['install'] += 1
        if calls['install'] == 1: raise native.NativeError('temporarily unavailable',503)
        return {'ok':True}
    monkeypatch.setattr(rt, 'wx_request', fake_wx)
    monkeypatch.setattr(native, 'request', fake_bridge)
    pairing = second.post('/asrobot/api/wechat/pairings',json={}).json()['id']
    assert second.post('/asrobot/api/wechat/pairings/'+pairing,json={}).status_code == 503
    with s.db() as db:
        assert db.execute('SELECT status FROM pairings WHERE id=?',(pairing,)).fetchone()[0] == 'installing'
        assert db.execute('SELECT status FROM bindings WHERE user_id=?',(uid,)).fetchone()[0] == 'installing'
    assert b'synthetic-secret' not in (s.DATA/'platform.sqlite3').read_bytes()
    assert second.delete('/asrobot/api/wechat/pairings/'+pairing).status_code == 400
    resumed = second.post('/asrobot/api/wechat/pairings/'+pairing,json={})
    assert resumed.status_code == 200 and resumed.json()['status'] == 'confirmed'
    assert calls == {'poll':1, 'install':2}
    with s.db() as db:
        assert db.execute('SELECT status FROM bindings WHERE user_id=?',(uid,)).fetchone()[0] == 'bound'
        assert not db.execute("SELECT 1 FROM secrets WHERE user_id=? AND kind='native_pairing_pending'",(uid,)).fetchone()
    assert first.post('/asrobot/api/wechat/pairings/'+pairing,json={}).status_code == 400


def test_instance_cannot_claim_another_users_wechat(clients, monkeypatch):
    first, second = clients
    users = [client.get('/asrobot/api/me').json()['user']['id'] for client in clients]
    for uid, ident in zip(users, ('1'*32,'2'*32)):
        with s.db() as db: db.execute("INSERT INTO assistant_instances VALUES(?,?,?,'active')",(ident,uid,s.now()))
    async def fake_wx(base,path,**kwargs):
        if path.endswith('get_bot_qrcode'): return {'qrcode':'qr','qrcode_img_content':'https://example.test/qr'}
        return {'status':'confirmed','bot_token':'secret','ilink_bot_id':'same-bot','ilink_user_id':'same-user','baseurl':rt.WX}
    async def fake_bridge(uid,endpoint,body=None): return {'running':False} if endpoint == 'status' else {'ok':True}
    monkeypatch.setattr(rt,'wx_request',fake_wx)
    monkeypatch.setattr(native,'request',fake_bridge)
    ids=[client.post('/asrobot/api/wechat/pairings',json={}).json()['id'] for client in clients]
    assert first.post('/asrobot/api/wechat/pairings/'+ids[0],json={}).status_code == 200
    assert second.post('/asrobot/api/wechat/pairings/'+ids[1],json={}).status_code == 400


def test_new_pairing_waits_until_legacy_wechat_ownership_is_known(clients, monkeypatch):
    legacy, new_user = clients
    old_uid = legacy.get('/asrobot/api/me').json()['user']['id']
    new_uid = new_user.get('/asrobot/api/me').json()['user']['id']
    s.workspace(old_uid)
    with s.db() as db:
        db.execute("UPDATE workspaces SET native_source='friday_full' WHERE user_id=?", (old_uid,))
        db.execute("INSERT INTO assistant_instances VALUES(?,?,?,'active')", ('f'*32, new_uid, s.now()))
    async def forbidden(*args, **kwargs): raise AssertionError('must not start an unverified pairing')
    monkeypatch.setattr(rt, 'wx_request', forbidden)
    assert new_user.post('/asrobot/api/wechat/pairings', json={}).status_code == 409
