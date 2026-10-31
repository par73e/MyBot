import asyncio
import importlib.util
import json
import tempfile
from pathlib import Path

spec = importlib.util.spec_from_file_location('instance_supervisor', Path(__file__).parents[1] / 'nativebridge' / 'instance_supervisor.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def test_supervisor_limits_control_and_never_starts_a_second_process(tmp_path):
    async def exercise():
        executable = tmp_path / 'fake-assistant'
        executable.write_text('#!/bin/sh\nexec sleep 30\n')
        executable.chmod(0o700)
        token = 'synthetic-token-' + 'x' * 40
        supervisor = module.Supervisor(str(executable), str(tmp_path), token, tmp_path)
        socket_dir = tempfile.TemporaryDirectory(prefix='asrobot-test-', dir='/tmp')
        sock = Path(socket_dir.name) / 'control.sock'
        server = await asyncio.start_unix_server(supervisor.handle, path=str(sock))
        async def call(key, action):
            reader, writer = await asyncio.open_unix_connection(str(sock))
            writer.write((json.dumps({'token': key, 'action': action}) + '\n').encode())
            await writer.drain()
            response = json.loads(await reader.readline())
            writer.close()
            await writer.wait_closed()
            return response
        try:
            assert (await call('other-token', 'start')) == {'ok': False}
            assert (await call(token, 'arbitrary-command')) == {'ok': False}
            assert (await call(token, 'status')) == {'ok': True, 'running': False, 'desired_running': False}
            assert (await call(token, 'start')) == {'ok': True, 'running': True, 'desired_running': True}
            pid = supervisor.bot.pid
            assert supervisor.desired_running()
            assert (await call(token, 'start')) == {'ok': True, 'running': True, 'desired_running': True}
            assert supervisor.bot.pid == pid
            await supervisor.stop(persist=False)
            assert supervisor.desired_running()
            assert (await call(token, 'start')) == {'ok': True, 'running': True, 'desired_running': True}
            assert (await call(token, 'stop')) == {'ok': True, 'running': False, 'desired_running': False}
            assert not supervisor.desired_running()
        finally:
            await supervisor.stop()
            server.close()
            await server.wait_closed()
            socket_dir.cleanup()
    asyncio.run(exercise())


def test_stale_socket_recovery_never_unlinks_files_or_live_servers(tmp_path):
    import socket
    import pytest
    with tempfile.TemporaryDirectory(prefix='asrobot-socket-', dir='/tmp') as directory:
        path = Path(directory) / 'control.sock'
        path.write_text('keep')
        with pytest.raises(ValueError): module.remove_stale_owned_socket(path)
        assert path.read_text() == 'keep'
        path.unlink()
        live = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        live.bind(str(path)); live.listen(1)
        try:
            with pytest.raises(ValueError, match='active'): module.remove_stale_owned_socket(path)
            assert path.exists()
        finally:
            live.close()
        module.remove_stale_owned_socket(path)
        assert not path.exists()


def test_workspace_identity_is_optional_and_rejects_unsafe_values(tmp_path):
    import pytest
    assert module.load_workspace_identity(tmp_path) is None
    identity = tmp_path / 'workspace-id'
    identity.write_text('/old/assistant/workspace')
    assert module.load_workspace_identity(tmp_path) == '/old/assistant/workspace'
    for value in ('relative', '/old/../other', '/old\nINJECT', '/' + 'a' * 4096):
        identity.write_text(value)
        with pytest.raises(ValueError):
            module.load_workspace_identity(tmp_path)


def test_supervisor_surfaces_bridge_exit_for_systemd_restart(monkeypatch):
    import pytest
    with tempfile.TemporaryDirectory(prefix='asr-supervisor-', dir='/tmp') as parent:
        root = Path(parent) / ('a' * 32)
        root.mkdir()
        for name in ('home', 'workspace', 'state'):
            (root / name).mkdir()
        (root / 'native.token').write_text('test-token-' + 'x' * 40)
        bridge = Path(parent) / 'bridge'
        bridge.write_text('#!/bin/sh\nexit 1\n')
        bridge.chmod(0o700)
        env = {'ASROBOT_INSTANCE_MODE': 'isolated', 'ASROBOT_RUNTIME_ROOT': str(root),
               'ASROBOT_RUNTIME_HOME': str(root / 'home'), 'ASROBOT_RUNTIME_WORKSPACE': str(root / 'workspace'),
               'ASROBOT_RUNTIME_STATE': str(root / 'state'), 'ASROBOT_RUNTIME_TOKEN': str(root / 'native.token'),
               'ASROBOT_RUNTIME_SOCKET': str(root / 'native.sock'), 'ASROBOT_RUNTIME_EXECUTABLE': '/bin/true',
               'ASROBOT_BRIDGE_EXECUTABLE': str(bridge), 'ASROBOT_RUNTIME_UNIT': 'asrobot-instance-' + root.name + '.service'}
        for key, value in env.items():
            monkeypatch.setenv(key, value)
        with pytest.raises(RuntimeError, match='bridge stopped'):
            asyncio.run(module.main())
        assert not (root / 'state' / 'control.sock').exists()
