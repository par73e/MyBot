import asyncio
import httpx
from app import native, runtime as rt, store as s
from test_platform import clients


def test_two_users_route_to_owned_instances_and_skip_platform_scheduler(clients, tmp_path, monkeypatch):
    first, second = clients
    users = [c.get('/asrobot/api/me').json()['user']['id'] for c in (first, second)]
    ids = ['1' * 32, '2' * 32]
    root = tmp_path / 'instances'
    monkeypatch.setenv('ASROBOT_INSTANCE_ROOT', str(root))
    for index, (uid, instance_id) in enumerate(zip(users, ids)):
        folder = root / instance_id
        folder.mkdir(parents=True)
        (folder / 'native.token').write_text('test-credential-' + str(index))
        with s.db() as db:
            db.execute("INSERT INTO assistant_instances VALUES(?,?,?,'active')", (instance_id, uid, s.now()))
    seen = []
    def transport(*, uds):
        instance_id = uds.split('/')[-2]
        index = ids.index(instance_id)
        def handle(request):
            assert request.headers['authorization'] == 'Bearer test-credential-' + str(index)
            assert request.url.params['source'] == 'friday_full'
            seen.append(instance_id)
            return httpx.Response(200, json={'instance': instance_id})
        return httpx.MockTransport(handle)
    monkeypatch.setattr(native.httpx, 'AsyncHTTPTransport', transport)
    for client, instance_id in zip((first, second), ids):
        assert client.get('/asrobot/api/workspace').json()['native_mode']
        assert client.get('/asrobot/api/native/status?source=other').json()['instance'] == instance_id
    assert seen == ids
    async def forbidden(*args, **kwargs):
        raise AssertionError('platform scheduler attempted a duplicate send')
    monkeypatch.setattr(rt, 'send', forbidden)
    for uid in users:
        asyncio.run(rt.tick_user(uid))


def test_invalid_instance_identifier_fails_closed(clients):
    _, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    with s.db() as db:
        db.execute("INSERT INTO assistant_instances VALUES(?,?,?,'active')", ('../other', uid, s.now()))
    assert user.get('/asrobot/api/native/status').status_code == 503


def test_reserved_instances_are_idempotent_and_do_not_switch_runtime(clients):
    from app.instance_registry import reserve
    _, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    s.workspace(uid)
    instance_id = reserve(uid)
    assert len(instance_id) == 32 and reserve(uid) == instance_id
    assert not user.get('/asrobot/api/workspace').json()['native_mode']
    assert user.get('/asrobot/api/native/status').status_code == 404


def test_existing_user_data_cannot_be_silently_attached(clients):
    from app.instance_registry import reserve
    import pytest
    _, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    s.workspace(uid)
    assert user.post('/asrobot/api/items/task', json={'title': 'existing'}).status_code == 200
    with pytest.raises(ValueError, match='separate migration'):
        reserve(uid)
    with s.db() as db:
        assert db.execute('SELECT count(*) FROM assistant_instances').fetchone()[0] == 0


def test_legacy_owner_reservation_preserves_existing_runtime_and_data(clients):
    import pytest
    from app.instance_registry import reserve_legacy_owner
    first, second = clients
    owner = first.get('/asrobot/api/me').json()['user']['id']
    other = second.get('/asrobot/api/me').json()['user']['id']
    s.workspace(owner)
    s.workspace(other)
    with s.db() as db:
        db.execute("UPDATE workspaces SET legacy_source='friday_owner',native_source='friday_full' WHERE user_id=?", (owner,))
    with pytest.raises(ValueError, match='legacy owner'):
        reserve_legacy_owner(other)
    identity = reserve_legacy_owner(owner)
    assert reserve_legacy_owner(owner) == identity
    assert s.native(owner) == 'friday_full'
    with s.db() as db:
        db.execute("INSERT INTO bindings(user_id,external_id,status) VALUES(?,?,'bound')", (owner, 'synthetic-wechat'))
    with pytest.raises(ValueError, match='reconciliation'):
        reserve_legacy_owner(owner)
    assert s.native(owner) == 'friday_full'


def test_registered_user_waits_for_isolated_instance_before_writing(clients, monkeypatch):
    from fastapi.testclient import TestClient
    from test_platform import ORIGIN
    from app.main import app
    monkeypatch.setenv('ASROBOT_AUTOPROVISION', '1')
    admin, _ = clients
    invite = admin.post('/asrobot/api/admin/invites', json={}).json()['invite']
    with TestClient(app, base_url=ORIGIN) as user:
        user.headers['Origin'] = ORIGIN
        assert user.post('/asrobot/api/register', json={'login':'newuser','password':'test-password-123','invite':invite,'agree':True}).status_code == 200
        login = user.post('/asrobot/api/login', json={'login':'newuser','password':'test-password-123'})
        user.headers['X-CSRF-Token'] = login.json()['csrf']
        assert user.get('/asrobot/api/workspace').json()['instance_pending'] is True
        assert user.get('/asrobot/api/settings').status_code == 503
        assert user.post('/asrobot/api/items/task', json={'title':'cannot race setup'}).status_code == 503
        uid = user.get('/asrobot/api/me').json()['user']['id']
        with s.db() as db:
            assert not db.execute('SELECT 1 FROM settings WHERE user_id=?', (uid,)).fetchone()
            assert not db.execute('SELECT 1 FROM entities WHERE user_id=?', (uid,)).fetchone()


def test_pending_inventory_only_contains_clean_new_accounts(clients, monkeypatch, capsys):
    import json
    import sys
    from app import instance_admin
    from app.instance_registry import reserve, reserve_legacy_owner
    first, second = clients
    owner = first.get('/asrobot/api/me').json()['user']['id']
    new_user = second.get('/asrobot/api/me').json()['user']['id']
    s.workspace(owner)
    s.workspace(new_user)
    with s.db() as db:
        db.execute("UPDATE workspaces SET legacy_source='friday_owner',native_source='friday_full' WHERE user_id=?", (owner,))
    reserve_legacy_owner(owner)
    new_instance = reserve(new_user)
    monkeypatch.setattr(sys, 'argv', ['instance_admin', 'list-pending'])
    instance_admin.main()
    assert json.loads(capsys.readouterr().out) == [{'instance_id':new_instance, 'user_id':new_user}]
    monkeypatch.setattr(sys, 'argv', ['instance_admin', 'status', new_user])
    instance_admin.main()
    assert json.loads(capsys.readouterr().out) == {'instance_id':new_instance, 'state':'reserved'}
    with s.db() as db:
        db.execute('INSERT INTO settings(user_id,body,revision) VALUES(?,?,1)', (new_user, '{}'))
    monkeypatch.setattr(sys, 'argv', ['instance_admin', 'list-pending'])
    instance_admin.main()
    assert json.loads(capsys.readouterr().out) == []


def test_prepare_two_blank_instances_does_not_copy_data_or_start(clients, tmp_path):
    import json
    import stat
    from app.instance_registry import reserve
    from app.instance_scaffold import prepare
    root = tmp_path / 'private-instances'
    root.mkdir(mode=0o700)
    users = [c.get('/asrobot/api/me').json()['user']['id'] for c in clients]
    ids = []
    for uid in users:
        s.workspace(uid)
        instance_id = reserve(uid)
        assert prepare(uid, root) == instance_id
        ids.append(instance_id)
        directory = root / instance_id
        assert stat.S_IMODE(directory.stat().st_mode) == 0o700
        assert stat.S_IMODE((directory / 'native.token').stat().st_mode) == 0o600
        assert json.loads((directory / 'instance.json').read_text())['running'] is False
        assert (directory / 'home' / 'config.toml').is_file()
        assert (directory / 'home' / 'prompts' / 'assistant.md').is_file()
        assert not (directory / 'home' / '.env').exists()
        assert list((directory / 'workspace').iterdir()) == []
        assert s.native(uid) is None
    assert ids[0] != ids[1]
    assert (root / ids[0] / 'native.token').read_bytes() != (root / ids[1] / 'native.token').read_bytes()


def test_preparation_refuses_existing_or_insecure_directories(clients, tmp_path):
    import pytest
    from app.instance_registry import reserve
    from app.instance_scaffold import prepare
    _, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    s.workspace(uid)
    instance_id = reserve(uid)
    root = tmp_path / 'instances'
    root.mkdir(mode=0o755)
    with pytest.raises(ValueError, match='private'):
        prepare(uid, root)
    root.chmod(0o700)
    (root / instance_id).mkdir()
    with pytest.raises(FileExistsError):
        prepare(uid, root)


def test_activation_requires_ready_bridge_and_empty_user(clients, tmp_path, monkeypatch):
    import pytest
    from app.instance_registry import reserve, activate_empty
    from app.instance_scaffold import prepare
    _, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    s.workspace(uid)
    instance_id = reserve(uid)
    root = tmp_path / 'instances'
    root.mkdir(mode=0o700)
    monkeypatch.setenv('ASROBOT_INSTANCE_ROOT', str(root))
    prepare(uid, root)
    with pytest.raises(ValueError, match='differs'):
        asyncio.run(activate_empty(uid, tmp_path / 'other'))
    def fake_transport(*, uds):
        assert uds == str(root / instance_id / 'native.sock')
        return httpx.MockTransport(lambda request: httpx.Response(200, json={'status': 'ok'} if request.url.path == '/health' else {'running': False, 'desired_running': False}))
    monkeypatch.setattr(httpx, 'AsyncHTTPTransport', fake_transport)
    assert asyncio.run(activate_empty(uid, root)) == instance_id
    assert user.get('/asrobot/api/workspace').json()['native_mode']
    with pytest.raises(ValueError, match='reserved'):
        asyncio.run(activate_empty(uid, root))


def test_activation_does_not_overwrite_user_settings(clients, tmp_path, monkeypatch):
    import pytest
    from app.instance_registry import reserve, activate_empty
    from app.instance_scaffold import prepare
    _, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    s.workspace(uid)
    reserve(uid)
    root = tmp_path / 'instances'
    root.mkdir(mode=0o700)
    monkeypatch.setenv('ASROBOT_INSTANCE_ROOT', str(root))
    prepare(uid, root)
    with s.db() as db:
        db.execute('INSERT INTO settings(user_id,body,revision) VALUES(?,?,?)', (uid, '{"name":"Mine"}', 1))
    monkeypatch.setattr(httpx, 'AsyncHTTPTransport', lambda **kw: httpx.MockTransport(lambda request: httpx.Response(200, json={'status': 'ok'} if request.url.path == '/health' else {'running': False, 'desired_running': False})))
    with pytest.raises(ValueError, match='separate migration'):
        asyncio.run(activate_empty(uid, root))
    assert not user.get('/asrobot/api/workspace').json()['native_mode']


def test_activation_allows_new_user_to_configure_a_stopped_assistant(clients, tmp_path, monkeypatch):
    import pytest
    from app.instance_registry import reserve, activate_empty
    from app.instance_scaffold import prepare
    _, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    s.workspace(uid)
    reserve(uid)
    root = tmp_path / 'instances'
    root.mkdir(mode=0o700)
    monkeypatch.setenv('ASROBOT_INSTANCE_ROOT', str(root))
    prepare(uid, root)
    monkeypatch.setattr(httpx, 'AsyncHTTPTransport', lambda **kw: httpx.MockTransport(lambda request: httpx.Response(200, json={'status': 'ok'} if request.url.path == '/health' else {'running': False, 'desired_running': False})))
    instance_id = asyncio.run(activate_empty(uid, root))
    assert s.native(uid) == 'instance:' + instance_id


def test_activation_rejects_a_bot_that_will_restart(clients, tmp_path, monkeypatch):
    import pytest
    from app.instance_registry import reserve, activate_empty
    from app.instance_scaffold import prepare
    _, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    s.workspace(uid)
    reserve(uid)
    root = tmp_path / 'instances'
    root.mkdir(mode=0o700)
    monkeypatch.setenv('ASROBOT_INSTANCE_ROOT', str(root))
    prepare(uid, root)
    monkeypatch.setattr(httpx, 'AsyncHTTPTransport', lambda **kw: httpx.MockTransport(lambda request: httpx.Response(200, json={'status':'ok'} if request.url.path == '/health' else {'running':False,'desired_running':True})))
    with pytest.raises(ValueError, match='not ready'):
        asyncio.run(activate_empty(uid, root))
    assert s.native(uid) is None


def test_legacy_attachment_is_atomic_and_preserves_existing_settings(clients, tmp_path, monkeypatch):
    import pytest
    from app.instance_registry import reserve_legacy_owner, attach_stopped_legacy_owner
    first, second = clients
    owner = first.get('/asrobot/api/me').json()['user']['id']
    other = second.get('/asrobot/api/me').json()['user']['id']
    s.workspace(owner)
    with s.db() as db:
        db.execute("UPDATE workspaces SET legacy_source='friday_owner',native_source='friday_full' WHERE user_id=?", (owner,))
        db.execute('INSERT INTO settings(user_id,body,revision) VALUES(?,?,1)', (owner, '{"name":"keep"}'))
    identity = reserve_legacy_owner(owner)
    with s.db() as db:
        db.execute("UPDATE users SET status='maintenance' WHERE id=?", (owner,))
        db.execute("INSERT INTO bindings(user_id,external_id,status) VALUES(?,?,'bound')", (other, 'synthetic-account'))
    root = tmp_path / 'instances'
    (root / identity).mkdir(parents=True)
    (root / identity / 'native.token').write_text('x' * 48)
    monkeypatch.setenv('ASROBOT_INSTANCE_ROOT', str(root))
    desired = [True]
    def transport(**kwargs):
        def response(request):
            if request.url.path == '/health': return httpx.Response(200, json={'status':'ok'})
            if request.url.path == '/v1/status': return httpx.Response(200, json={'running':False,'desired_running':desired[0]})
            return httpx.Response(200, json={'account':'synthetic-account','credential_configured':True})
        return httpx.MockTransport(response)
    monkeypatch.setattr(httpx, 'AsyncHTTPTransport', transport)
    with pytest.raises(ValueError, match='could not be verified'):
        asyncio.run(attach_stopped_legacy_owner(owner, root, 'synthetic-account'))
    desired[0] = False
    with pytest.raises(ValueError, match='reconciliation'):
        asyncio.run(attach_stopped_legacy_owner(owner, root, 'synthetic-account'))
    assert s.native(owner) == 'friday_full'
    with s.db() as db:
        assert db.execute('SELECT status FROM users WHERE id=?', (owner,)).fetchone()[0] == 'maintenance'
        db.execute('DELETE FROM bindings WHERE user_id=?', (other,))
    assert asyncio.run(attach_stopped_legacy_owner(owner, root, 'synthetic-account')) == identity
    assert s.native(owner) == 'instance:' + identity
    assert s.settings(owner)['name'] == 'keep'
    with s.db() as db:
        assert db.execute('SELECT status FROM users WHERE id=?', (owner,)).fetchone()[0] == 'active'
        assert tuple(db.execute('SELECT legacy_source,native_source FROM workspaces WHERE user_id=?', (owner,)).fetchone()) == (None, None)
        assert db.execute('SELECT external_id FROM bindings WHERE user_id=?', (owner,)).fetchone()[0] == 'synthetic-account'


def test_mail_setup_routes_only_to_owner_and_never_logs_secret(clients, tmp_path, monkeypatch):
    first, second = clients
    users = [client.get('/asrobot/api/me').json()['user']['id'] for client in clients]
    root = tmp_path/'instances'
    monkeypatch.setenv('ASROBOT_INSTANCE_ROOT',str(root))
    identities = ['a'*32,'b'*32]
    for uid, identity in zip(users, identities):
        folder=root/identity;folder.mkdir(parents=True)
        (folder/'native.token').write_text('token-'+identity)
        with s.db() as db:
            db.execute("INSERT INTO assistant_instances VALUES(?,?,?,'active')",(identity,uid,s.now()))
    calls=[]
    def transport(*,uds):
        identity=uds.split('/')[-2]
        def handle(request):
            assert request.headers['authorization']=='Bearer token-'+identity
            calls.append((identity,request.url.path))
            return httpx.Response(200,json={'revision':'new','accounts':[],'proxy_available':False})
        return httpx.MockTransport(handle)
    monkeypatch.setattr(httpx,'AsyncHTTPTransport',transport)
    payload={'revision':'old','provider':'qq','address':'sample@example.test','authorization_code':'synthetic-private-code'}
    assert second.post('/asrobot/api/native/mail-accounts',json=payload).status_code==200
    assert first.get('/asrobot/api/native/mail-accounts').status_code==200
    assert calls==[(identities[1],'/v1/mail-accounts'),(identities[0],'/v1/mail-accounts')]
    with s.db() as db:
        events=' '.join(row[0] for row in db.execute('SELECT event FROM activity'))
    assert 'synthetic-private-code' not in events


def test_each_user_can_update_only_own_assistant_identity(clients, tmp_path, monkeypatch):
    first, second = clients
    users = [client.get('/asrobot/api/me').json()['user']['id'] for client in clients]
    ids = ['a' * 32, 'b' * 32]
    root = tmp_path / 'instances'
    root.mkdir(mode=0o700)
    monkeypatch.setenv('ASROBOT_INSTANCE_ROOT', str(root))
    values = {}
    for uid, instance_id in zip(users, ids):
        (root / instance_id).mkdir()
        (root / instance_id / 'native.token').write_text('token-' + instance_id)
        with s.db() as db:
            db.execute("INSERT INTO assistant_instances VALUES(?,?,?,'active')", (instance_id, uid, s.now()))
        values[instance_id] = {'name': '', 'address': '', 'revision': 'one'}
    def transport(*, uds):
        instance_id = uds.split('/')[-2]
        def handle(request):
            assert request.headers['authorization'] == 'Bearer token-' + instance_id
            if request.method == 'POST':
                data = __import__('json').loads(request.content)
                if data['revision'] != values[instance_id]['revision']:
                    return httpx.Response(409, json={'error': 'revision changed'})
                values[instance_id] = {'name': data['name'], 'address': data['address'], 'revision': 'two'}
            return httpx.Response(200, json=values[instance_id])
        return httpx.MockTransport(handle)
    monkeypatch.setattr(native.httpx, 'AsyncHTTPTransport', transport)
    assert first.post('/asrobot/api/native/identity', json={'name':'助手甲','address':'甲','revision':'one'}).status_code == 200
    assert second.post('/asrobot/api/native/identity', json={'name':'助手乙','address':'乙','revision':'one'}).status_code == 200
    assert first.get('/asrobot/api/native/identity').json()['name'] == '助手甲'
    assert second.get('/asrobot/api/native/identity').json()['name'] == '助手乙'
    assert first.post('/asrobot/api/native/identity', json={'name':'overwrite','address':'','revision':'one'}).status_code == 409
    assert second.post('/asrobot/api/native/identity', json={'name':'bad','address':'','revision':'two'}, headers={'X-CSRF-Token':'bad'}).status_code == 403


def test_managed_account_cannot_be_deleted_without_instance_cleanup(clients, monkeypatch):
    _, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    with s.db() as db:
        db.execute("INSERT INTO assistant_instances VALUES(?,?,?,'active')", ('a' * 32, uid, s.now()))
    async def forbidden(*args, **kwargs):
        raise AssertionError('must not pause or orphan a managed assistant')
    monkeypatch.setattr(native, 'request', forbidden)
    response = user.request('DELETE', '/asrobot/api/account', json={'confirm':'bob', 'password':'test-password-123'})
    assert response.status_code == 409
    assert '尚未开放' in response.json()['detail']
    with s.db() as db:
        assert db.execute('SELECT 1 FROM users WHERE id=?', (uid,)).fetchone()
        assert db.execute('SELECT 1 FROM assistant_instances WHERE user_id=?', (uid,)).fetchone()


def test_managed_settings_use_current_assistant_identity(clients, monkeypatch):
    _, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    with s.db() as db:
        db.execute("INSERT INTO assistant_instances VALUES(?,?,?,'active')", ('b' * 32, uid, s.now()))
    async def fake(uid, endpoint, *args, **kwargs):
        if endpoint == 'identity': return {'name': '小星', 'address': '主人'}
        if endpoint == 'profile': return {'prompt':'hello', 'model_ref':'provider/model', 'providers':[], 'key_configured':False}
        raise AssertionError(endpoint)
    monkeypatch.setattr(native, 'request', fake)
    result = user.get('/asrobot/api/settings').json()
    assert result['name'] == '小星' and result['address'] == '主人'
    assert result['migration_info'] is None


def test_existing_disabled_account_requires_migration_before_enable(clients, monkeypatch):
    admin, user = clients
    uid = user.get('/asrobot/api/me').json()['user']['id']
    assert user.post('/asrobot/api/items/task',json={'title':'existing data'}).status_code == 200
    assert admin.post('/asrobot/api/admin/users/'+uid,json={'action':'disable'}).status_code == 200
    monkeypatch.setenv('ASROBOT_AUTOPROVISION','1')
    result=admin.post('/asrobot/api/admin/users/'+uid,json={'action':'enable'})
    assert result.status_code == 409 and '先迁移' in result.json()['detail']
    with s.db() as db:
        assert db.execute('SELECT status FROM users WHERE id=?',(uid,)).fetchone()[0]=='disabled'
        assert db.execute('SELECT count(*) FROM entities WHERE user_id=?',(uid,)).fetchone()[0]==1
