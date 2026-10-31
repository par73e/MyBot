import hashlib
import importlib.util
import json
import os
import sqlite3
from pathlib import Path
from types import SimpleNamespace
import pytest

spec = importlib.util.spec_from_file_location('instance_migration_host', Path(__file__).parents[1] / 'deploy/instance_migration_host.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


@pytest.mark.skipif(os.geteuid() != 0, reason='root-owned fixture required; run in an isolated container')
def test_install_verified_candidate_into_stopped_empty_instance(tmp_path, monkeypatch):
    snapshots = {}
    for name in ('home','workspace','mail','weather'):
        path = tmp_path/name
        path.mkdir(mode=0o700)
        (path/'.asrobot-offline-snapshot').write_text('fixture')
        snapshots[name] = path
    home = snapshots['home']
    (home/'assistant').mkdir(mode=0o700)
    with sqlite3.connect(home/'assistant/state.sqlite3') as db:
        db.execute('CREATE TABLE assistant_entities(kind TEXT,id TEXT,payload TEXT)')
        db.execute('INSERT INTO assistant_entities VALUES(?,?,?)', ('job','job1',json.dumps({'id':'job1','workspaceRoot':'/old/workspace'})))
    (home/'config.toml').write_text('[bot.weixin]\nenabled=true\naccount_id="synthetic"\n[bot.allowlist]\nweixin_users=["owner"]\n')
    (home/'weixin').mkdir(mode=0o700)
    (home/'weixin/accounts').mkdir(mode=0o700)
    (home/'weixin/accounts/synthetic.json').write_text(json.dumps({'user_id':'owner','token':'synthetic'}))
    (home/'assistant/mail.json').write_text(json.dumps({'accounts':[{'provider':'gmail','proxy':'http://127.0.0.1:17890','credential_file':'/old/mail/token'}]}))
    (home/'assistant/weather.json').write_text(json.dumps({'credential_file':'/old/weather/key'}))
    (snapshots['mail']/'token').write_text('synthetic-mail')
    (snapshots['weather']/'key').write_text('synthetic-weather')
    old = {name:'/old/'+name for name in snapshots}
    identity = 'e'*32
    candidate = tmp_path/'candidate'
    module.migration.assemble_legacy_candidate(snapshots,old,candidate,'/var/lib/asrobot-instances/'+identity,'Example','Owner')
    root = tmp_path/'instances'
    root.mkdir(mode=0o711)
    target = root/identity
    target.mkdir(mode=0o700)
    for name in ('home','home/prompts','workspace','state'):(target/name).mkdir(mode=0o700)
    files = {'native.token':'x'*48,'instance.json':json.dumps({'version':1,'instance_id':identity,'status':'prepared','running':False}),
             'home/config.toml':'blank-config','home/prompts/assistant.md':'blank-prompt'}
    for name, content in files.items():
        p=target/name;p.write_text(content);p.chmod(0o600)
    monkeypatch.setattr(module, 'INSTANCE_ROOT', root)
    units=tmp_path/'units';units.mkdir()
    (units/('asrobot-instance-'+identity+'.service')).write_text(module.host.render_unit(identity,'8.8.8.8',17890))
    (units/('asrobot-mail-proxy-'+identity+'.service')).write_text(module.host.render_mail_proxy_unit(identity,17890))
    monkeypatch.setattr(module, 'UNIT_ROOT', units)
    monkeypatch.setattr(module.pwd, 'getpwnam', lambda name:SimpleNamespace(pw_uid=0,pw_gid=0))
    monkeypatch.setattr(module, '_no_processes', lambda uid:True)
    monkeypatch.setattr(module.host, 'BLANK_CONFIG_SHA256', hashlib.sha256(b'blank-config').hexdigest())
    monkeypatch.setattr(module.host, 'BLANK_PROMPT_SHA256', hashlib.sha256(b'blank-prompt').hexdigest())
    calls = []
    def execute(args, **kwargs):
        calls.append(args)
        if args[1] == 'show':return SimpleNamespace(stdout=(str(units/args[2]) if args[-2]=='--property=FragmentPath' else 'no')+'\n')
        if args[1] == 'is-active':return SimpleNamespace(stdout='inactive\n')
        if args[1] == 'is-enabled':return SimpleNamespace(stdout='static\n' if 'mail-proxy' in args[2] else 'disabled\n')
        if args[0].endswith('getfacl'):
            entries = ['user::rw-','user:10001:r--','group::---','mask::r--','other::---'] if args[-1].endswith('native.token') else ['user::rwx','user:10001:--x','group::---','mask::--x','other::---']
            return SimpleNamespace(stdout='\n'.join(entries)+'\n\n')
        return SimpleNamespace(stdout='')
    def active_service(args, **kwargs):
        if args[1] == 'is-active':return SimpleNamespace(stdout='active\n')
        return execute(args, **kwargs)
    with pytest.raises(ValueError, match='not stopped'):
        module.install_staged(snapshots,old,candidate,identity,'Example','Owner','8.8.8.8',execute=active_service)
    assert (target/'home/config.toml').read_text() == 'blank-config'
    result = module.install_staged(snapshots,old,candidate,identity,'Example','Owner','8.8.8.8',execute=execute)
    assert result['state'] == 'installed-and-stopped'
    assert (target/'state/workspace-id').read_text() == '/old/workspace'
    assert (target/'native.token').read_text() == 'x'*48
    assert (target/'external/mail/token').read_text() == 'synthetic-mail'
    assert (home/'assistant/mail.json').is_file()
    assert not any('start' in args or 'enable' in args for args in calls)
    with pytest.raises(ValueError, match='not empty'):
        module.install_staged(snapshots,old,candidate,identity,'Example','Owner','8.8.8.8',execute=execute)
