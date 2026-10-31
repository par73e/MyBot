import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
import pytest

spec=importlib.util.spec_from_file_location('instance_network',Path(__file__).parents[1]/'deploy'/'instance_network.py')
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)


def test_network_rules_are_installed_before_creation():
    calls=[]
    def run(args,check=True):
        calls.append(args)
        if args[:4] == ['docker','network','ls','--quiet']: return SimpleNamespace(returncode=0,stdout='')
        if '-C' in args or args[:3] == ['ip','link','show']: return SimpleNamespace(returncode=1,stdout='')
        return SimpleNamespace(returncode=0,stdout='')
    value=module.prepare('a'*32,run)
    create=next(i for i,c in enumerate(calls) if c[:3]==['docker','network','create'])
    rules=[i for i,c in enumerate(calls) if '-I' in c]
    assert len(rules)==1+len(module.PRIVATE_V4) and max(rules)<create
    assert value['bridge']=='asr'+'a'*10


def test_network_rejects_mismatch_before_changing_firewall():
    calls=[]
    def run(args,check=True):
        calls.append(args)
        if args[:4] == ['docker','network','ls','--quiet']: return SimpleNamespace(returncode=0,stdout='id')
        if args[:3]==['docker','network','inspect']:
            return SimpleNamespace(returncode=0,stdout=json.dumps([{'Name':'asrobot-instance-'+'b'*32,'Driver':'bridge','Options':{},'Labels':{}}]))
        return SimpleNamespace(returncode=0,stdout='')
    with pytest.raises(ValueError,match='does not match'):
        module.prepare('b'*32,run)
    assert not any('-I' in c or '-D' in c for c in calls)


def test_network_failure_never_removes_guard_rules():
    calls=[]
    def run(args,check=True):
        calls.append(args)
        if args[:4] == ['docker','network','ls','--quiet']: return SimpleNamespace(returncode=0,stdout='')
        if '-C' in args or args[:3] == ['ip','link','show']: return SimpleNamespace(returncode=1,stdout='')
        if args[:3] == ['docker','network','create']: raise RuntimeError('daemon unavailable')
        return SimpleNamespace(returncode=0,stdout='')
    with pytest.raises(RuntimeError):module.prepare('c'*32,run)
    assert any('-I' in c for c in calls) and not any('-D' in c for c in calls)


def test_network_rejects_untrusted_identifiers():
    for value in ('../other','A'*32,'a'*31,'a'*33):
        with pytest.raises(ValueError):module.names(value)
