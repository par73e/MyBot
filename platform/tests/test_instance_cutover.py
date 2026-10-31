import importlib.util
import json
import os
from pathlib import Path
import shutil
import sqlite3
from types import SimpleNamespace
import pytest

spec = importlib.util.spec_from_file_location('instance_cutover', Path(__file__).parents[1] / 'deploy/instance_cutover.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


@pytest.mark.skipif(os.geteuid() != 0, reason='root-owned fixture required; run in an isolated container')
def test_snapshot_requires_quiescence_and_never_marks_live_data(tmp_path, monkeypatch):
    import hashlib
    live=tmp_path/'live';live.mkdir(mode=0o700)
    for name in ('reasonix','workspace','mail','weather'):
        path=live/name;path.mkdir(mode=0o700)
        (path/'sample').write_text('synthetic')
        os.chown(path/'sample',12346,12346)
        os.chown(path,12346,12346)
    database=sqlite3.connect(live/'reasonix/session.sqlite')
    database.execute('PRAGMA journal_mode=WAL')
    database.execute('PRAGMA wal_autocheckpoint=0')
    database.execute('CREATE TABLE sessions(value TEXT)')
    database.execute("INSERT INTO sessions VALUES('synthetic')")
    database.commit()
    for file in (live/'reasonix').iterdir():os.chown(file,12346,12346)
    digest=lambda:{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in (live/'reasonix').iterdir() if p.is_file()}
    before=digest()
    backups=tmp_path/'backups';backups.mkdir(mode=0o700)
    platform=tmp_path/'platform-data';platform.mkdir(mode=0o700)
    monkeypatch.setattr(module,'LEGACY_ROOT',live)
    monkeypatch.setattr(module,'BACKUP_ROOT',backups)
    monkeypatch.setattr(module,'DATA_ROOT',platform)
    monkeypatch.setattr(module.pwd,'getpwnam',lambda name:SimpleNamespace(pw_uid=12346))
    monkeypatch.setattr(module.installer,'_no_processes',lambda uid:True)
    container='c'*64;image='sha256:'+'d'*64
    def execute(args, **kwargs):
        if args[0].endswith('systemctl'):return SimpleNamespace(stdout='inactive\n' if args[1]=='is-active' else 'disabled\n')
        if args[1]=='inspect':return SimpleNamespace(stdout=json.dumps([{'Id':container,'Image':image,'State':{'Running':False}}]))
        if args[1:]==['ps','-q']:return SimpleNamespace(stdout='')
        raise AssertionError(args)
    def legacy_running(args, **kwargs):
        if args[0].endswith('systemctl') and args[1]=='is-active':return SimpleNamespace(stdout='active\n')
        return execute(args, **kwargs)
    with pytest.raises(ValueError,match='stopped and disabled'):
        module.snapshot_stopped_legacy(container,image,execute=legacy_running)
    assert not list(backups.iterdir())
    result=module.snapshot_stopped_legacy(container,image,execute=execute)
    snapshot=Path(result['snapshot_directory'])
    assert result['snapshot_count']==4
    assert all((snapshot/name/'.asrobot-offline-snapshot').is_file() for name in ('home','workspace','mail','weather'))
    assert not (live/'reasonix/.asrobot-offline-snapshot').exists()
    assert (snapshot/'home/sample').read_text()=='synthetic'
    assert digest()==before
    database.close()
    (live/'reasonix/sample').write_text('changed-after-snapshot')
    with pytest.raises(ValueError,match='does not match'):
        module.plan.verify_private_copy(snapshot/'home',live/'reasonix',_candidate_uid=12346)


@pytest.mark.skipif(os.geteuid() != 0, reason='root-owned fixture required; run in an isolated container')
def test_offline_cutover_is_atomic_and_retains_a_private_database_backup(tmp_path, monkeypatch):
    snapshots = {}
    for name in ('home','workspace','mail','weather'):
        path=tmp_path/('snapshot-'+name);path.mkdir(mode=0o700)
        (path/'.asrobot-offline-snapshot').write_text('fixture')
        snapshots[name]=path
    home=snapshots['home']
    (home/'assistant').mkdir(mode=0o700)
    (home/'weixin').mkdir(mode=0o700)
    (home/'weixin/accounts').mkdir(mode=0o700)
    (home/'config.toml').write_text('[bot.weixin]\nenabled=true\naccount_id="synthetic"\n[bot.allowlist]\nweixin_users=["owner"]\n')
    (home/'weixin/accounts/synthetic.json').write_text(json.dumps({'user_id':'owner','token':'synthetic-token'}))
    (home/'assistant/mail.json').write_text(json.dumps({'accounts':[{'credential_file':'/legacy/mail/token'}]}))
    (home/'assistant/weather.json').write_text(json.dumps({'credential_file':'/legacy/weather/key'}))
    (snapshots['mail']/'token').write_text('synthetic mail')
    (snapshots['weather']/'key').write_text('synthetic weather')
    live=tmp_path/'live';live.mkdir(mode=0o700)
    old={name:str(live/name) for name in snapshots}
    # The configuration maps the legacy location, not the test fixture root.
    (home/'assistant/mail.json').write_text(json.dumps({'accounts':[{'provider':'gmail','proxy':'http://127.0.0.1:17890','credential_file':old['mail']+'/token'}]}))
    (home/'assistant/weather.json').write_text(json.dumps({'credential_file':old['weather']+'/key'}))
    with sqlite3.connect(home/'assistant/state.sqlite3') as db:
        db.execute('CREATE TABLE assistant_entities(kind TEXT,id TEXT,payload TEXT)')
        db.execute('INSERT INTO assistant_entities VALUES(?,?,?)',('job','j',json.dumps({'id':'j','workspaceRoot':old['workspace']})))
    for name,source in snapshots.items():
        shutil.copytree(source,live/name)
        (live/name/'.asrobot-offline-snapshot').unlink()
        for parent,dirs,files in os.walk(live/name):
            os.chown(parent,12346,12346)
            for child in files:os.chown(Path(parent)/child,12346,12346)
    ident='a'*32; uid='b'*32
    candidate=tmp_path/'candidate'
    module.plan.assemble_legacy_candidate(snapshots,old,candidate,'/var/lib/asrobot-instances/'+ident,'Example','Owner')
    instances=tmp_path/'instances';instances.mkdir(mode=0o711)
    target=instances/ident;target.mkdir(mode=0o700)
    for part in ('home','workspace','external','state'):
        shutil.copytree(candidate/part,target/part)
        for parent,dirs,files in os.walk(target/part):
            os.chown(parent,12345,12345)
            for child in files:os.chown(Path(parent)/child,12345,12345)
    (target/'native.token').write_text('x'*48)
    os.chown(target,12345,12345)
    platform=tmp_path/'platform-data';platform.mkdir(mode=0o700)
    db_path=platform/'platform.sqlite3'
    with sqlite3.connect(db_path) as db:
        db.executescript('CREATE TABLE users(id TEXT PRIMARY KEY,status TEXT); CREATE TABLE workspaces(id TEXT PRIMARY KEY,user_id TEXT,legacy_source TEXT,native_source TEXT); CREATE TABLE bindings(user_id TEXT PRIMARY KEY,external_id TEXT UNIQUE,status TEXT,last_poll INTEGER DEFAULT 0); CREATE TABLE settings(user_id TEXT,body TEXT);')
        db.execute('INSERT INTO users VALUES(?,?)',(uid,'active'))
        db.execute('INSERT INTO workspaces VALUES(?,?,?,?)',('workspace',uid,'friday_owner','friday_full'))
        db.execute('INSERT INTO settings VALUES(?,?)',(uid,'keep'))
    db_path.chmod(0o600)
    backups=tmp_path/'backups';backups.mkdir(mode=0o700)
    units=tmp_path/'units';units.mkdir(mode=0o700)
    (units/('asrobot-instance-'+ident+'.service')).write_text(module.host.render_unit(ident,'8.8.8.8',17890))
    (units/('asrobot-mail-proxy-'+ident+'.service')).write_text(module.host.render_mail_proxy_unit(ident,17890))
    monkeypatch.setattr(module,'DATA_ROOT',platform)
    monkeypatch.setattr(module,'INSTANCE_ROOT',instances)
    monkeypatch.setattr(module,'BACKUP_ROOT',backups)
    monkeypatch.setattr(module,'LEGACY_ROOT',live)
    monkeypatch.setattr(module,'UNIT_ROOT',units)
    monkeypatch.setattr(module.pwd,'getpwnam',lambda name:SimpleNamespace(pw_uid=12346 if name=='mybot' else 12345,pw_gid=12345))
    monkeypatch.setattr(module.installer,'_no_processes',lambda uid:True)
    container='c'*64; image='sha256:'+'d'*64
    def execute(args, **kwargs):
        if args[0].endswith('systemctl'):
            if args[1]=='show':return SimpleNamespace(stdout=(str(units/args[2]) if args[-2]=='--property=FragmentPath' else 'no')+'\n')
            running=args[2].startswith(('asrobot-instance-','asrobot-mail-proxy-')) or args[2]=='mybot-proxy.service'
            return SimpleNamespace(stdout=('active\n' if running else 'inactive\n') if args[1]=='is-active' else 'disabled\n')
        if args[1]=='inspect':return SimpleNamespace(stdout=json.dumps([{'Id':container,'Image':image,'State':{'Running':False}}]))
        if args[1:]==['ps','-q']:return SimpleNamespace(stdout='')
        raise AssertionError(args)
    def bridge(path,token,endpoint):
        return {'running':False,'desired_running':False} if 'status' in endpoint else {'account':'synthetic','credential_configured':True}
    probes=[]
    def probe(root,port,process_uid,process_gid,execute):probes.append(port)
    def legacy_running(args, **kwargs):
        if args[0].endswith('systemctl') and args[1]=='is-active':return SimpleNamespace(stdout='active\n')
        return execute(args, **kwargs)
    with pytest.raises(ValueError, match='stopped and disabled'):
        module.commit_offline(snapshots,old,candidate,ident,uid,'Example','Owner','8.8.8.8',container,image,execute=legacy_running,bridge_get=bridge,proxy_probe=probe)
    with sqlite3.connect(db_path) as db:
        db.execute("INSERT INTO bindings(user_id,external_id,status) VALUES('other','synthetic','bound')")
    with pytest.raises(ValueError, match='reconciliation'):
        module.commit_offline(snapshots,old,candidate,ident,uid,'Example','Owner','8.8.8.8',container,image,execute=execute,bridge_get=bridge,proxy_probe=probe)
    with sqlite3.connect(db_path) as db:
        assert not db.execute("SELECT 1 FROM sqlite_master WHERE name='assistant_instances'").fetchone()
        assert db.execute('SELECT legacy_source,native_source FROM workspaces WHERE user_id=?',(uid,)).fetchone()==('friday_owner','friday_full')
        db.execute("DELETE FROM bindings WHERE user_id='other'")
    result=module.commit_offline(snapshots,old,candidate,ident,uid,'Example','Owner','8.8.8.8',container,image,execute=execute,bridge_get=bridge,proxy_probe=probe)
    assert probes and all(port==17890 for port in probes)
    assert result['state']=='ownership-committed' and result['assistant_running'] is False
    with sqlite3.connect(db_path) as db:
        assert db.execute('SELECT id,state FROM assistant_instances WHERE user_id=?',(uid,)).fetchone()==(ident,'active')
        assert db.execute('SELECT legacy_source,native_source FROM workspaces WHERE user_id=?',(uid,)).fetchone()==(None,None)
        assert db.execute('SELECT external_id FROM bindings WHERE user_id=?',(uid,)).fetchone()==('synthetic',)
        assert db.execute('SELECT body FROM settings WHERE user_id=?',(uid,)).fetchone()==('keep',)
    backup=Path(result['backup'])
    assert backup.stat().st_mode & 0o077 == 0
    with sqlite3.connect(backup) as db:
        assert db.execute('SELECT legacy_source,native_source FROM workspaces WHERE user_id=?',(uid,)).fetchone()==('friday_owner','friday_full')
    assert not any((target/'state'/name).exists() for name in ('desired.json',))
