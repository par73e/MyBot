from copy import deepcopy
import pytest
from app.migration_plan import remap_heartbeat_workspace


def test_task_remap_preserves_targets_delivery_history_and_original():
    original = {'schemaVersion': 1, 'tasks': [
        {'id': 'task-a', 'workspaceRoot': '/old/workspace', 'target': {'connection_id':'weixin','chat_id':'synthetic-a'},
         'enabled': True, 'at':'2026-11-01T09:00:00+08:00', 'history':[{'status':'delivered','startedAt':123}]},
        {'id': 'task-b', 'workspaceRoot': '/old/workspace', 'enabled':False, 'history':[]},
    ]}
    snapshot = deepcopy(original)
    result = remap_heartbeat_workspace(original, '/old/workspace', '/instances/a/workspace')
    assert original == snapshot
    assert all(t['workspaceRoot'] == '/instances/a/workspace' for t in result['tasks'])
    assert result['tasks'][0]['target'] == original['tasks'][0]['target']
    assert result['tasks'][0]['history'] == original['tasks'][0]['history']
    assert result['tasks'][1]['enabled'] is False
    assert remap_heartbeat_workspace(result, '/old/workspace', '/instances/a/workspace') == result


def test_remap_rejects_unknown_workspace_duplicate_ids_and_schema():
    with pytest.raises(ValueError, match='manual reconciliation'):
        remap_heartbeat_workspace({'schemaVersion':1,'tasks':[{'id':'x','workspaceRoot':'/foreign'}]}, '/old', '/new')
    with pytest.raises(ValueError, match='duplicate'):
        remap_heartbeat_workspace({'schemaVersion':1,'tasks':[{'id':'x','workspaceRoot':'/old'},{'id':'x','workspaceRoot':'/old'}]}, '/old', '/new')
    with pytest.raises(ValueError, match='unsupported'):
        remap_heartbeat_workspace({'schemaVersion':2,'tasks':[]}, '/old', '/new')
    with pytest.raises(ValueError, match='normalized'):
        remap_heartbeat_workspace({'schemaVersion':1,'tasks':[]}, '/old/../private', '/new')


def test_sqlite_staging_preserves_unrelated_rows_and_delivery_history():
    import json, sqlite3
    from app.migration_plan import remap_staged_sqlite_jobs
    db = sqlite3.connect(':memory:')
    db.execute('CREATE TABLE assistant_entities(kind TEXT,id TEXT,payload TEXT,PRIMARY KEY(kind,id))')
    task = {'id':'job-a','workspaceRoot':'/old','target':{'chat_id':'synthetic'},'history':[{'status':'delivered','startedAt':1}]}
    db.execute('INSERT INTO assistant_entities VALUES(?,?,?)', ('job','job-a',json.dumps(task)))
    db.execute('INSERT INTO assistant_entities VALUES(?,?,?)', ('todo','todo-a','{"title":"keep"}'))
    db.commit()
    assert remap_staged_sqlite_jobs(db, '/old', '/new') == {'examined':1,'updated':1}
    migrated = json.loads(db.execute("SELECT payload FROM assistant_entities WHERE kind='job'").fetchone()[0])
    assert migrated == dict(task, workspaceRoot='/new')
    assert db.execute("SELECT payload FROM assistant_entities WHERE kind='todo'").fetchone()[0] == '{"title":"keep"}'
    assert remap_staged_sqlite_jobs(db, '/old', '/new') == {'examined':1,'updated':0}


def test_sqlite_staging_rolls_back_all_jobs_on_foreign_root():
    import json, sqlite3
    from app.migration_plan import remap_staged_sqlite_jobs
    db = sqlite3.connect(':memory:')
    db.execute('CREATE TABLE assistant_entities(kind TEXT,id TEXT,payload TEXT,PRIMARY KEY(kind,id))')
    for ident, path in [('one','/old'), ('two','/foreign')]:
        db.execute('INSERT INTO assistant_entities VALUES(?,?,?)', ('job',ident,json.dumps({'id':ident,'workspaceRoot':path})))
    db.commit()
    with pytest.raises(ValueError, match='manual reconciliation'):
        remap_staged_sqlite_jobs(db, '/old', '/new')
    assert json.loads(db.execute("SELECT payload FROM assistant_entities WHERE id='one'").fetchone()[0])['workspaceRoot'] == '/old'


def test_offline_snapshot_stages_wal_database_without_modifying_source(tmp_path):
    import json, sqlite3
    from app.migration_plan import stage_offline_snapshot
    source = tmp_path / 'source'
    source.mkdir(mode=0o700)
    (source / '.asrobot-offline-snapshot').write_text('fixture')
    (source / 'assistant').mkdir()
    (source / 'empty-workspace').mkdir()
    (source / 'assistant' / 'profile.json').write_text('{"name":"Synthetic"}')
    connection = sqlite3.connect(source / 'assistant' / 'state.sqlite3')
    connection.execute('PRAGMA journal_mode=WAL')
    connection.execute('CREATE TABLE assistant_entities(kind TEXT,id TEXT,payload TEXT,PRIMARY KEY(kind,id))')
    job = {'id':'example','workspaceRoot':'/old/workspace','target':{'chat_id':'fake'},'history':[{'status':'delivered'}]}
    connection.execute('INSERT INTO assistant_entities VALUES(?,?,?)',('job','example',json.dumps(job)))
    connection.commit()
    destination = tmp_path / 'candidate'
    result = stage_offline_snapshot(source, destination, '/old/workspace', '/new/workspace')
    assert result['sqlite_databases'] == 1 and result['jobs'] == {'examined':1,'updated':1}
    with sqlite3.connect(destination / 'assistant' / 'state.sqlite3') as staged:
        payload = json.loads(staged.execute('SELECT payload FROM assistant_entities').fetchone()[0])
    assert payload == dict(job, workspaceRoot='/new/workspace')
    assert json.loads(connection.execute('SELECT payload FROM assistant_entities').fetchone()[0]) == job
    assert (destination/'empty-workspace').is_dir()
    assert not (destination/'.asrobot-offline-snapshot').exists()
    connection.close()


def test_snapshot_refuses_symlinks_and_existing_destinations(tmp_path):
    from app.migration_plan import stage_offline_snapshot
    source = tmp_path / 'source'
    source.mkdir(mode=0o700)
    (source / '.asrobot-offline-snapshot').write_text('fixture')
    (source / 'link').symlink_to('/tmp')
    destination = tmp_path / 'candidate'
    with pytest.raises(ValueError, match='symbolic'):
        stage_offline_snapshot(source, destination, '/old', '/new')
    assert not destination.exists()
    (source/'link').unlink()
    destination.mkdir()
    with pytest.raises(ValueError, match='must not exist'):
        stage_offline_snapshot(source, destination, '/old', '/new')


def test_staged_identity_never_overwrites_the_source_or_existing_profile(tmp_path):
    import json
    from app.migration_plan import set_staged_identity, stage_offline_snapshot
    source = tmp_path / 'source'
    source.mkdir(mode=0o700)
    (source / '.asrobot-offline-snapshot').write_text('fixture')
    (source / 'assistant').mkdir(mode=0o700)
    destination = tmp_path / 'candidate'
    stage_offline_snapshot(source, destination, '/old', '/new')
    with pytest.raises(ValueError):
        set_staged_identity(source, 'Example', 'Owner')
    with pytest.raises(ValueError):
        set_staged_identity(destination, 'bad\nname', 'Owner')
    set_staged_identity(destination, 'Example', 'Owner')
    assert json.loads((destination / 'assistant/profile.json').read_text()) == {'name': 'Example', 'address': 'Owner'}
    assert not (source / 'assistant/profile.json').exists()
    with pytest.raises(FileExistsError):
        set_staged_identity(destination, 'Other', 'Owner')


def test_staged_verification_detects_database_or_conversation_changes(tmp_path):
    import json
    import sqlite3
    from app.migration_plan import stage_offline_snapshot, verify_staged_snapshot
    source = tmp_path / 'source'
    source.mkdir(mode=0o700)
    (source / '.asrobot-offline-snapshot').write_text('fixture')
    (source / 'assistant').mkdir(mode=0o700)
    (source / 'projects').mkdir(mode=0o700)
    (source / 'projects' / 'session.jsonl').write_text('synthetic session')
    with sqlite3.connect(source / 'assistant/state.sqlite3') as db:
        db.execute('CREATE TABLE assistant_entities(kind TEXT,id TEXT,payload TEXT,PRIMARY KEY(kind,id))')
        db.execute('INSERT INTO assistant_entities VALUES(?,?,?)', ('job', 'one', json.dumps({'id':'one','workspaceRoot':'/old','history':['sent']})))
        db.execute('INSERT INTO assistant_entities VALUES(?,?,?)', ('memory', 'two', json.dumps({'body':'private synthetic'})))
    candidate = tmp_path / 'candidate'
    stage_offline_snapshot(source, candidate, '/old', '/new')
    assert verify_staged_snapshot(source, candidate, '/old', '/new') == {'database_rows': 2, 'conversation_files': 1, 'requires_credential_and_delivery_review': True}
    (candidate / 'projects/session.jsonl').write_text('changed')
    with pytest.raises(ValueError, match='conversation files differ'):
        verify_staged_snapshot(source, candidate, '/old', '/new')
    (candidate / 'projects/session.jsonl').write_text('synthetic session')
    with sqlite3.connect(candidate / 'assistant/state.sqlite3') as db:
        db.execute("UPDATE assistant_entities SET payload='{}' WHERE kind='memory'")
    with pytest.raises(ValueError, match='database content differs'):
        verify_staged_snapshot(source, candidate, '/old', '/new')


def test_staged_verification_compares_session_sqlite_wal_logically(tmp_path):
    import sqlite3
    from app.migration_plan import stage_offline_snapshot, verify_staged_snapshot
    source = tmp_path / 'source'
    source.mkdir(mode=0o700)
    (source / '.asrobot-offline-snapshot').write_text('fixture')
    (source / 'assistant').mkdir(mode=0o700)
    (source / 'projects').mkdir(mode=0o700)
    with sqlite3.connect(source / 'assistant/state.sqlite3') as db:
        db.execute('CREATE TABLE items(value TEXT)')
        db.execute("INSERT INTO items VALUES('assistant')")
    session = sqlite3.connect(source / 'projects/sessions.sqlite')
    try:
        session.execute('PRAGMA journal_mode=WAL')
        session.execute('PRAGMA wal_autocheckpoint=0')
        session.execute('CREATE TABLE sessions(value TEXT)')
        session.execute("INSERT INTO sessions VALUES('session')")
        session.commit()
        candidate = tmp_path / 'candidate'
        stage_offline_snapshot(source, candidate, '/old', '/new')
        result = verify_staged_snapshot(source, candidate, '/old', '/new')
        assert result['database_rows'] == 2 and result['conversation_files'] == 0
    finally:
        session.close()


def test_private_credential_copy_comparison_rejects_changes_and_links(tmp_path):
    from app.migration_plan import verify_private_copy
    source, candidate = tmp_path / 'source', tmp_path / 'candidate'
    source.mkdir(mode=0o700)
    candidate.mkdir(mode=0o700)
    (source / 'credential').write_text('synthetic credential')
    (candidate / 'credential').write_text('synthetic credential')
    assert verify_private_copy(source, candidate) == {'verified_files': 1, 'verified_databases': 0}
    (candidate / 'credential').write_text('different')
    with pytest.raises(ValueError, match='does not match'):
        verify_private_copy(source, candidate)
    (candidate / 'credential').unlink()
    (candidate / 'credential').symlink_to(source / 'credential')
    with pytest.raises(ValueError, match='unsupported'):
        verify_private_copy(source, candidate)


def test_staged_wechat_checks_one_owner_and_refuses_environment_override(tmp_path):
    import json
    from app.migration_plan import stage_offline_snapshot, verify_staged_wechat
    source = tmp_path / 'source'
    source.mkdir(mode=0o700)
    (source / '.asrobot-offline-snapshot').write_text('fixture')
    (source / 'config.toml').write_text('[bot.weixin]\nenabled = true\naccount_id = "test-account"\n[bot.allowlist]\nweixin_users = ["test-user"]\n')
    (source / 'weixin').mkdir(mode=0o700)
    (source / 'weixin/accounts').mkdir(mode=0o700)
    (source / 'weixin/accounts/test-account.json').write_text(json.dumps({'user_id':'test-user', 'token':'synthetic-token'}))
    (source / '.env').write_text('MODEL_API_KEY=synthetic\n')
    candidate = tmp_path / 'candidate'
    stage_offline_snapshot(source, candidate, '/old', '/new')
    assert verify_staged_wechat(source, candidate) == {'single_owner_matches': True, 'credential_matches': True, 'connection_tested': False}
    (candidate / '.env').write_text('export WEIXIN_BOT_TOKEN = other\n')
    with pytest.raises(ValueError, match='override'):
        verify_staged_wechat(source, candidate)


def test_staged_gmail_proxy_requires_one_uncredentialed_loopback_port(tmp_path):
    import json
    from app.migration_plan import staged_gmail_proxy_port
    root=tmp_path/'home';root.mkdir(mode=0o700)
    (root/'.asrobot-migration-candidate').write_text('')
    (root/'assistant').mkdir(mode=0o700)
    path=root/'assistant/mail.json'
    config={'accounts':[{'provider':'gmail','proxy':'http://127.0.0.1:17890'}, {'provider':'gmail','proxy':'http://127.0.0.1:17890'}, {'provider':'qq'}]}
    path.write_text(json.dumps(config))
    assert staged_gmail_proxy_port(root)==17890
    config['accounts'][1]['proxy']='http://127.0.0.1:9999'
    path.write_text(json.dumps(config))
    with pytest.raises(ValueError, match='multiple'):
        staged_gmail_proxy_port(root)
    config['accounts'][1]['proxy']='http://user:secret@127.0.0.1:17890'
    path.write_text(json.dumps(config))
    with pytest.raises(ValueError, match='requires review'):
        staged_gmail_proxy_port(root)


def test_credentials_path_remap_preserves_account_data_and_source():
    from app.migration_plan import remap_credential_config
    source = {'accounts':[{'id':'a','credential_file':'/old/mail/a/token.json','address':'synthetic@example.test'}]}
    before = deepcopy(source)
    result = remap_credential_config(source,'mail',[('/old/mail','/instance/external/mail')])
    assert source == before
    assert result['accounts'][0]['credential_file'] == '/instance/external/mail/a/token.json'
    assert result['accounts'][0]['address'] == before['accounts'][0]['address']
    weather = {'credential_file':'/old/weather/key.json','locations_file':'/old/home/assistant/locations.json'}
    assert remap_credential_config(weather,'weather',[('/old/weather','/instance/external/weather'),('/old/home','/instance/home')]) == {'credential_file':'/instance/external/weather/key.json','locations_file':'/instance/home/assistant/locations.json'}


def test_credentials_path_remap_fails_closed_for_unknown_or_escaping_paths():
    from app.migration_plan import remap_credential_config
    for path in ('/other/token','/old/mail/../outside','relative/token'):
        with pytest.raises(ValueError):
            remap_credential_config({'accounts':[{'credential_file':path}]},'mail',[('/old/mail','/instance/mail')])


def test_staged_credential_rewrite_is_scoped_and_idempotent(tmp_path):
    import json
    from app.migration_plan import stage_offline_snapshot, rewrite_staged_credential_configs
    source = tmp_path / 'source'
    source.mkdir(mode=0o700)
    (source / '.asrobot-offline-snapshot').write_text('fixture')
    (source / 'assistant').mkdir(mode=0o700)
    mail = {'accounts':[{'id':'one','credential_file':'/old/mail/token'}]}
    weather = {'credential_file':'/old/weather/key'}
    (source / 'assistant/mail.json').write_text(json.dumps(mail))
    (source / 'assistant/weather.json').write_text(json.dumps(weather))
    candidate = tmp_path / 'candidate'
    stage_offline_snapshot(source, candidate, '/old/workspace', '/instance/workspace')
    mappings = [('/old/mail','/instance/external/mail'), ('/old/weather','/instance/external/weather')]
    assert rewrite_staged_credential_configs(source, candidate, '/instance', mappings)['updated_configs'] == 2
    assert rewrite_staged_credential_configs(source, candidate, '/instance', mappings)['updated_configs'] == 0
    assert json.loads((source / 'assistant/mail.json').read_text()) == mail
    assert json.loads((candidate / 'assistant/mail.json').read_text())['accounts'][0]['credential_file'] == '/instance/external/mail/token'
    with pytest.raises(ValueError, match='inside'):
        rewrite_staged_credential_configs(source, candidate, '/instance', [('/old/mail','/elsewhere')])
    (candidate / 'assistant/weather.json').write_text('{}')
    with pytest.raises(ValueError, match='unexpectedly'):
        rewrite_staged_credential_configs(source, candidate, '/instance', mappings)


def test_assemble_legacy_candidate_preserves_data_and_never_starts(tmp_path):
    import json
    import sqlite3
    from app.migration_plan import assemble_legacy_candidate, verify_legacy_candidate
    snapshots = {}
    for key in ('home', 'workspace', 'mail', 'weather'):
        root = tmp_path / key
        root.mkdir(mode=0o700)
        (root / '.asrobot-offline-snapshot').write_text('fixture')
        snapshots[key] = root
    home = snapshots['home']
    (home / 'assistant').mkdir(mode=0o700)
    with sqlite3.connect(home / 'assistant/state.sqlite3') as db:
        db.execute('CREATE TABLE assistant_entities(kind TEXT,id TEXT,payload TEXT,PRIMARY KEY(kind,id))')
        db.execute('INSERT INTO assistant_entities VALUES(?,?,?)', ('job','one',json.dumps({'id':'one','workspaceRoot':'/old/workspace','history':['sent']})))
    (home / 'config.toml').write_text('[bot.weixin]\nenabled = true\naccount_id = "bot"\n[bot.allowlist]\nweixin_users = ["user"]\n')
    (home / 'weixin').mkdir(mode=0o700)
    (home / 'weixin/accounts').mkdir(mode=0o700)
    (home / 'weixin/accounts/bot.json').write_text(json.dumps({'user_id':'user','token':'synthetic'}))
    (home / 'assistant/mail.json').write_text(json.dumps({'accounts':[{'credential_file':'/old/mail/token'}]}))
    (home / 'assistant/weather.json').write_text(json.dumps({'credential_file':'/old/weather/key'}))
    (home / 'projects').mkdir(mode=0o700)
    (home / 'skills/follow-builders').mkdir(mode=0o700, parents=True)
    (home / 'skills/follow-builders/SKILL.md').write_text('Run: sh /old/home/skills/follow-builders/scripts/run-friday.sh')
    (home / 'projects/session.jsonl').write_text('synthetic history')
    (snapshots['workspace'] / 'note.txt').write_text('synthetic workspace')
    (snapshots['mail'] / 'token').write_text('synthetic mail credential')
    (snapshots['weather'] / 'key').write_text('synthetic weather credential')
    old = {key:'/old/' + key for key in snapshots}
    planned = '/var/lib/asrobot-instances/' + 'a' * 32
    destination = tmp_path / 'candidate'
    result = assemble_legacy_candidate(snapshots, old, destination, planned, 'Example', 'Owner')
    assert result['running'] is False and result['requires_live_cutover_review'] is True
    assert result['verification']['wechat']['single_owner_matches'] is True
    assert result['verification']['conversation_files'] == 1
    assert (destination / 'state/workspace-id').read_text() == old['workspace']
    assert json.loads((destination / 'home/assistant/profile.json').read_text()) == {'name':'Example','address':'Owner'}
    assert not (home / 'assistant/profile.json').exists()
    assert not (destination / 'state/desired.json').exists()
    assert '${REASONIX_HOME}/skills/follow-builders/scripts/run-friday.sh' in (destination / 'home/skills/follow-builders/SKILL.md').read_text()
    assert '/old/home/' in (home / 'skills/follow-builders/SKILL.md').read_text()
    assert verify_legacy_candidate(snapshots, old, destination, planned, 'Example', 'Owner')['wechat']['credential_matches'] is True
    (destination / 'home/assistant/weather.json').write_text('{}')
    with pytest.raises(ValueError, match='credential configuration differs'):
        verify_legacy_candidate(snapshots, old, destination, planned, 'Example', 'Owner')
    with pytest.raises(ValueError, match='new private'):
        assemble_legacy_candidate(snapshots, old, destination, planned, 'Example', 'Owner')
