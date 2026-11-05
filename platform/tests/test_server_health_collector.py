import asyncio
import importlib.util
import os
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch


def load(monkeypatch):
    monkeypatch.setenv('FRIDAY_HEALTH_INSTANCE', 'a' * 32)
    with patch('pwd.getpwnam', return_value=SimpleNamespace(pw_uid=os.getuid())):
        spec = importlib.util.spec_from_file_location('health_collector', Path(__file__).parents[1] / 'nativebridge/server_health_collector.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
    return module


def test_collect_unknown_without_docker_or_network(monkeypatch):
    m = load(monkeypatch)
    monkeypatch.setattr(m.subprocess, 'run', lambda *a, **k: SimpleNamespace(stdout='active\n'))
    class Blocked:
        def open(self, *args, **kwargs): raise OSError('offline')
    monkeypatch.setattr(m.urllib.request, 'build_opener', lambda *a: Blocked())
    result = m.collect()
    assert 'asrobot_container' not in result['items']
    assert result['items']['security']['status'] == 'unknown'
    assert result['items']['website']['status'] == 'unknown'
    assert result['items']['friday']['status'] == 'ok'
    assert result['status'] == 'unknown'


def test_reject_wrong_peer_and_invalid_command(monkeypatch):
    m = load(monkeypatch)
    monkeypatch.setattr(m.socket, 'SO_PEERCRED', getattr(m.socket, 'SO_PEERCRED', 17), raising=False)
    class Sock:
        def __init__(self, uid): self.uid = uid
        def getsockopt(self, *args): return m.struct.pack('3i', 1, self.uid, 1)
    class Writer:
        def __init__(self, uid): self.sock=Sock(uid);self.data=b''
        def get_extra_info(self, name): return self.sock
        def write(self, data): self.data+=data
        async def drain(self): pass
        def close(self): pass
        async def wait_closed(self): pass
    async def run(uid, request):
        r=asyncio.StreamReader();r.feed_data(request);r.feed_eof();w=Writer(uid)
        await m.handle(r,w);return w.data
    assert asyncio.run(run(os.getuid()+1,b'status\n')) == b''
    assert asyncio.run(run(os.getuid(),b'restart\n')) == b''


def test_activity_freshness_and_unavailable_not_zero(monkeypatch, tmp_path):
    import datetime
    import json
    m = load(monkeypatch)
    p = tmp_path / 'snapshot.json'
    monkeypatch.setattr(m, 'ACTIVITY', p)
    assert m.activity_items()['access']['status'] == 'unknown'
    now = datetime.datetime.now(datetime.timezone.utc)
    data = {'schema_version': 1, 'collected_at': now.isoformat(), 'items': {
        'security': {'status': 'ok', 'value': {'jails': []}},
        'access': {'status': 'ok', 'value': {'requests': 0}}}}
    p.write_text(json.dumps(data))
    assert m.activity_items()['access']['value']['requests'] == 0
    data['collected_at'] = (now - datetime.timedelta(minutes=11)).isoformat()
    p.write_text(json.dumps(data))
    assert m.activity_items()['access']['status'] == 'unknown'
