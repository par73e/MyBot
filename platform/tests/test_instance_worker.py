import importlib.util
import json
import subprocess
from pathlib import Path
from types import SimpleNamespace
import pytest

spec = importlib.util.spec_from_file_location('instance_worker', Path(__file__).parents[1] / 'deploy' / 'instance_worker.py')
worker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(worker)

CONFIG = {'server_ipv4': '8.8.8.8', 'container_id': 'a' * 64, 'image_id': 'sha256:' + 'b' * 64}
RECORD = {'instance_id': 'c' * 32, 'user_id': 'd' * 32}


def test_preflight_enforces_real_sandbox_and_network_properties():
    calls = []
    worker.preflight(RECORD['instance_id'], CONFIG['server_ipv4'], execute=lambda args, **kw: calls.append(args))
    args = calls[0]
    assert 'NoNewPrivileges=yes' in args
    assert 'IPAddressDeny=127.0.0.0/8' in args
    assert 'IPAddressDeny=8.8.8.8/32' in args
    assert '/usr/bin/bwrap' in args[args.index('-c') + 1]
    assert args[args.index('--uid') + 1] == 'asr-' + RECORD['instance_id'][:20]


def test_worker_refuses_unapproved_platform_container():
    calls = []
    def execute(args, **kwargs):
        calls.append(args)
        return SimpleNamespace(stdout=json.dumps([{'Id':'f' * 64, 'Image':CONFIG['image_id'], 'State':{'Running':True}}]))
    with pytest.raises(ValueError, match='approved build'):
        worker.platform_command(CONFIG, ['list-pending'], execute=execute)
    assert len(calls) == 1 and calls[0][1] == 'inspect'


def test_preflight_fails_if_instance_can_reach_host_listener():
    import socket
    def compromised_runner(args, **kwargs):
        with socket.create_connection(('127.0.0.1', int(args[-1])), timeout=1):
            pass
    with pytest.raises(ValueError, match='reach the host'):
        worker.preflight(RECORD['instance_id'], CONFIG['server_ipv4'], execute=compromised_runner)


def test_worker_prepares_once_and_recovers_lost_activation_response(tmp_path, monkeypatch):
    calls = []
    monkeypatch.setattr(worker, 'preflight', lambda ident, ip, **kw: calls.append(['preflight', ident]))
    def execute(args, **kw):
        calls.append(args)
        if args[1:3] == ['inspect', 'asrobot']:
            return SimpleNamespace(stdout=json.dumps([{'Id':CONFIG['container_id'], 'Image':CONFIG['image_id'], 'State':{'Running':True}}]))
        if 'activate' in args:
            raise subprocess.CalledProcessError(1, args)
        if 'status' in args:
            return SimpleNamespace(stdout=json.dumps({'instance_id':RECORD['instance_id'], 'state':'active'}))
        return SimpleNamespace(stdout='')
    prepared = []
    result = worker.process_pending(RECORD, CONFIG, tmp_path, execute=execute, prepare=lambda *args: prepared.append(args), sleep=lambda _:None)
    assert result == {'state':'active'} and len(prepared) == 1
    assert json.loads((tmp_path / (RECORD['instance_id'] + '.json')).read_text()) == {'state':'active'}
    assert not any('disable' in args for args in calls)
    with pytest.raises(FileExistsError):
        worker.process_pending(RECORD, CONFIG, tmp_path, execute=execute, prepare=lambda *args: None)


def test_worker_stops_prepared_service_when_activation_fails(tmp_path, monkeypatch):
    calls = []
    monkeypatch.setattr(worker, 'preflight', lambda *args, **kw: None)
    def execute(args, **kw):
        calls.append(args)
        if args[1:3] == ['inspect', 'asrobot']:
            return SimpleNamespace(stdout=json.dumps([{'Id':CONFIG['container_id'], 'Image':CONFIG['image_id'], 'State':{'Running':True}}]))
        if 'activate' in args or 'status' in args:
            raise subprocess.CalledProcessError(1, args)
        return SimpleNamespace(stdout='')
    with pytest.raises(subprocess.CalledProcessError):
        worker.process_pending(RECORD, CONFIG, tmp_path, execute=execute, prepare=lambda *args:None, sleep=lambda _:None)
    assert any(args[1:3] == ['disable', '--now'] for args in calls)
    assert json.loads((tmp_path / (RECORD['instance_id'] + '.json')).read_text()) == {'state':'preparing'}
