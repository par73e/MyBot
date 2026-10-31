import importlib.util
import json
import os
import subprocess
from pathlib import Path
from types import SimpleNamespace
import pytest

spec=importlib.util.spec_from_file_location('instance_launcher',Path(__file__).parents[1]/'deploy'/'instance_launcher.py')
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)

@pytest.fixture
def prepared(tmp_path, monkeypatch):
    instance_id='a'*32
    root=tmp_path/'instances';root.mkdir(mode=0o700)
    folder=root/instance_id;folder.mkdir(mode=0o700)
    for sub in ('home','workspace','state','home/prompts'):(folder/sub).mkdir(mode=0o700,exist_ok=True)
    (folder/'instance.json').write_text(json.dumps({'version':1,'instance_id':instance_id,'status':'prepared','running':False}))
    (folder/'native.token').write_text('synthetic-'+'x'*40)
    (folder/'home/config.toml').write_text('synthetic')
    (folder/'home/prompts/assistant.md').write_text('synthetic')
    for p in (folder/'instance.json',folder/'native.token',folder/'home/config.toml',folder/'home/prompts/assistant.md'):p.chmod(0o600)
    real=Path.lstat
    def fake_stat(path):
        value=real(path)
        if path == folder or folder in path.parents:
            fields=list(value);fields[4]=10001;fields[5]=10001
            return os.stat_result(fields)
        return value
    monkeypatch.setattr(Path,'lstat',fake_stat)
    return root,instance_id


def test_create_only_stopped_unprivileged_container_after_guard(prepared):
    root,instance_id=prepared; calls=[]; image_id='sha256:'+'c'*64
    def run(args,check=True):
        calls.append(args)
        if args[:3]==['docker','image','inspect']:
            return SimpleNamespace(stdout=json.dumps([{'Id':image_id,'Config':{'User':'10001:10001','Labels':{'asrobot.contract':module.CONTRACT}}}]),returncode=0)
        return SimpleNamespace(stdout='',returncode=0)
    def guard(value):
        calls.append(['guard',value]);return {'network':'test-network'}
    result=module.create(instance_id,root,image_id,run,guard)
    command=next(c for c in calls if c[:2]==['docker','create'])
    assert calls.index(['guard',instance_id]) < calls.index(command)
    assert '--read-only' in command and command[command.index('--restart')+1]=='no'
    assert command[command.index('--user')+1]=='10001:10001'
    assert '--privileged' not in command and not any('docker.sock' in item for item in command)
    assert result['state']=='created'


def test_create_fails_closed_on_unverified_image(prepared):
    root,instance_id=prepared
    def run(args,check=True):return SimpleNamespace(stdout=json.dumps([{'Id':'sha256:'+'d'*64,'Config':{'User':'10001:10001','Labels':{'asrobot.contract':module.CONTRACT}}}]),returncode=0)
    with pytest.raises(ValueError,match='not verified'):module.create(instance_id,root,'sha256:'+'c'*64,run)


def test_create_refuses_runtime_without_workspace_sandbox(prepared):
    root,instance_id=prepared; image_id='sha256:'+'c'*64; calls=[]
    def run(args,check=True):
        calls.append(args)
        if args[:3]==['docker','image','inspect']:
            return SimpleNamespace(stdout=json.dumps([{'Id':image_id,'Config':{'User':'10001:10001','Labels':{'asrobot.contract':module.CONTRACT}}}]),returncode=0)
        if args[:2]==['docker','run']: raise subprocess.CalledProcessError(1,args)
        return SimpleNamespace(stdout='',returncode=0)
    with pytest.raises(ValueError,match='sandbox is unavailable'):
        module.create(instance_id,root,image_id,run,lambda ident:pytest.fail('network must not be created'))
    assert not any(call[:2]==['docker','create'] for call in calls)
