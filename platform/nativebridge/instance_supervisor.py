"""Per-instance supervisor. Only an authenticated local bridge can control it."""
import asyncio
import errno
import hmac
import json
import os
from pathlib import Path
import re
import signal
import socket
import stat
import tempfile


class Supervisor:
    def __init__(self, executable, workspace, token, state=None):
        self.executable, self.workspace, self.token = executable, workspace, token
        self.state = Path(state) if state is not None else None
        self.bot = None
        self.lock = asyncio.Lock()

    def running(self):
        return self.bot is not None and self.bot.returncode is None

    def desired_running(self):
        if self.state is None:
            return False
        try:
            raw = (self.state / 'desired.json').read_bytes()
            if len(raw) > 1024:
                raise ValueError('invalid desired state')
            value = json.loads(raw)
            if not isinstance(value, dict) or set(value) != {'running'} or not isinstance(value['running'], bool):
                raise ValueError('invalid desired state')
            return value['running']
        except FileNotFoundError:
            return False

    def save_desired(self, running):
        if self.state is None:
            return
        fd, name = tempfile.mkstemp(prefix='.desired-', dir=self.state)
        try:
            with os.fdopen(fd, 'w') as out:
                json.dump({'running': running}, out)
                out.flush()
                os.fsync(out.fileno())
            os.replace(name, self.state / 'desired.json')
        finally:
            if os.path.exists(name):
                os.unlink(name)

    async def stop(self, persist=True):
        if persist:
            self.save_desired(False)
        if self.running():
            self.bot.terminate()
            try:
                await asyncio.wait_for(self.bot.wait(), 10)
            except asyncio.TimeoutError:
                self.bot.kill()
                await self.bot.wait()
        self.bot = None

    async def start(self):
        if self.running():
            return
        self.bot = await asyncio.create_subprocess_exec(
            self.executable, 'bot', 'start', '--channels', 'weixin',
            '--heartbeat', '--heartbeat-poll', '1s', '--dir', self.workspace,
            stdin=asyncio.subprocess.DEVNULL,
        )
        # Refuse to advertise a process that fails immediately at startup.
        try:
            await asyncio.wait_for(self.bot.wait(), 0.5)
        except asyncio.TimeoutError:
            try:
                self.save_desired(True)
            except OSError:
                await self.stop(persist=False)
                raise
            return
        self.bot = None
        raise RuntimeError('assistant failed to start')

    async def maintain(self):
        while True:
            await asyncio.sleep(5)
            async with self.lock:
                if self.desired_running() and not self.running():
                    try:
                        await self.start()
                    except (OSError, RuntimeError):
                        # Retain desired state and retry later without starting
                        # a second process while the previous one is alive.
                        pass

    async def handle(self, reader, writer):
        try:
            raw = await asyncio.wait_for(reader.readline(), 5)
            if len(raw) > 4096:
                raise ValueError('request too large')
            request = json.loads(raw)
            if not isinstance(request, dict) or set(request) != {'token', 'action'}:
                raise ValueError('invalid request')
            token = request['token']
            if not isinstance(token, str) or not hmac.compare_digest(token, self.token):
                raise ValueError('denied')
            action = request['action']
            if action not in ('status', 'start', 'stop', 'restart'):
                raise ValueError('invalid action')
            async with self.lock:
                if action in ('stop', 'restart'):
                    await self.stop()
                if action in ('start', 'restart'):
                    await self.start()
                response = {'ok': True, 'running': self.running(), 'desired_running': self.desired_running()}
        except (ValueError, TypeError, json.JSONDecodeError, asyncio.TimeoutError, RuntimeError, OSError):
            response = {'ok': False}
        writer.write((json.dumps(response) + '\n').encode())
        try:
            await writer.drain()
        finally:
            writer.close()
            await writer.wait_closed()


def remove_stale_owned_socket(path):
    """Remove only our own inactive socket, preserving files and live servers."""
    try:
        original = path.lstat()
    except FileNotFoundError:
        return
    if not stat.S_ISSOCK(original.st_mode) or original.st_uid != os.getuid():
        raise ValueError('control path is not an owned socket')
    probe = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    probe.settimeout(0.2)
    try:
        probe.connect(str(path))
    except OSError as exc:
        if exc.errno not in (errno.ECONNREFUSED, errno.ENOENT):
            raise ValueError('control socket state is uncertain') from exc
    else:
        raise ValueError('another supervisor is active')
    finally:
        probe.close()
    try:
        current = path.lstat()
    except FileNotFoundError:
        return
    if current.st_ino != original.st_ino or current.st_dev != original.st_dev:
        raise ValueError('control socket changed during recovery')
    path.unlink()


def load_workspace_identity(state):
    """Keep migrated session lookup stable without changing an owner's data."""
    path = state / 'workspace-id'
    try:
        fd = os.open(path, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0))
    except FileNotFoundError:
        return None
    with os.fdopen(fd, 'rb') as stream:
        raw = stream.read(4097)
    if len(raw) > 4096:
        raise ValueError('invalid workspace identity')
    value = raw.decode('utf-8')
    if not value.startswith('/') or os.path.normpath(value) != value or '\x00' in value or '\n' in value:
        raise ValueError('invalid workspace identity')
    return value


async def main():
    if os.environ.get('ASROBOT_INSTANCE_MODE') != 'isolated':
        raise SystemExit('isolated instance mode required')
    root = Path(os.environ['ASROBOT_RUNTIME_ROOT'])
    workspace = os.environ['ASROBOT_RUNTIME_WORKSPACE']
    state = Path(os.environ['ASROBOT_RUNTIME_STATE'])
    executable = os.environ['ASROBOT_RUNTIME_EXECUTABLE']
    bridge = os.environ['ASROBOT_BRIDGE_EXECUTABLE']
    for value in (root, Path(workspace), state, Path(executable), Path(bridge)):
        if not value.is_absolute():
            raise SystemExit('absolute paths required')
    if not re.fullmatch(r'[0-9a-f]{32}', root.name) or os.environ.get('ASROBOT_RUNTIME_UNIT') != 'asrobot-instance-' + root.name + '.service':
        raise SystemExit('invalid instance identity')
    for key in ('ASROBOT_RUNTIME_HOME', 'ASROBOT_RUNTIME_WORKSPACE', 'ASROBOT_RUNTIME_STATE', 'ASROBOT_RUNTIME_TOKEN', 'ASROBOT_RUNTIME_SOCKET'):
        value = Path(os.environ[key])
        if not value.is_absolute() or value == root or root not in value.parents or '..' in value.parts:
            raise SystemExit('instance paths must remain inside the instance root')
    identity = load_workspace_identity(state)
    if identity is not None:
        os.environ['REASONIX_CONVERSATION_WORKSPACE_ID'] = identity
    else:
        os.environ.pop('REASONIX_CONVERSATION_WORKSPACE_ID', None)
    token = Path(os.environ['ASROBOT_RUNTIME_TOKEN']).read_text().strip()
    if len(token) < 32:
        raise SystemExit('private instance token required')
    control = state / 'control.sock'
    # Recover only an inactive socket owned by this instance's process user.
    remove_stale_owned_socket(control)
    supervisor = Supervisor(executable, workspace, token, state)
    server = await asyncio.start_unix_server(supervisor.handle, path=str(control))
    os.chmod(control, 0o600)
    socket_identity = control.lstat()
    process = None
    maintenance = None
    bridge_wait = None
    stopping = None
    try:
        process = await asyncio.create_subprocess_exec(bridge, stdin=asyncio.subprocess.DEVNULL)
        if supervisor.desired_running():
            await supervisor.start()
        maintenance = asyncio.create_task(supervisor.maintain())
        loop = asyncio.get_running_loop()
        stop = asyncio.Event()
        for sig in (signal.SIGINT, signal.SIGTERM):
            loop.add_signal_handler(sig, stop.set)
        bridge_wait = asyncio.create_task(process.wait())
        stopping = asyncio.create_task(stop.wait())
        done, _ = await asyncio.wait((bridge_wait, stopping, maintenance), return_when=asyncio.FIRST_COMPLETED)
        if not stop.is_set():
            if maintenance in done:
                maintenance.result()
                raise RuntimeError('assistant supervision stopped unexpectedly')
            raise RuntimeError('assistant bridge stopped unexpectedly')
    finally:
        if maintenance:
            maintenance.cancel()
            await asyncio.gather(maintenance, return_exceptions=True)
        await supervisor.stop(persist=False)
        if process and process.returncode is None:
            process.terminate()
            await process.wait()
        server.close()
        await server.wait_closed()
        try:
            current = control.lstat()
            if current.st_ino == socket_identity.st_ino and current.st_dev == socket_identity.st_dev:
                control.unlink()
        except FileNotFoundError:
            pass
        if stopping: stopping.cancel()
        if bridge_wait: bridge_wait.cancel()


if __name__ == '__main__':
    asyncio.run(main())
