"""Root-only, bounded provisioning of reserved empty assistant instances.

Run from a trusted systemd timer after installing reviewed binaries and the
platform container. It never imports legacy data or starts a WeChat bot.
Unfinished attempts are retained for operator reconciliation, not retried.
"""
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import re
import socket
import stat
import subprocess
import time

spec = importlib.util.spec_from_file_location('instance_host', Path(__file__).with_name('instance_host.py'))
host = importlib.util.module_from_spec(spec)
spec.loader.exec_module(host)


def run(args, *, check=True, timeout=60):
    return subprocess.run(args, check=check, capture_output=True, text=True, timeout=timeout)


def read_config(path=Path('/etc/asrobot-instance-worker.json')):
    info = path.lstat()
    if path.is_symlink() or not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077:
        raise ValueError('worker configuration must be a private root-owned file')
    config = json.loads(path.read_text())
    required = {'server_ipv4', 'container_id', 'image_id'}
    if not isinstance(config, dict) or not required.issubset(config) or set(config) - required - {'mail_proxy_port'}:
        raise ValueError('invalid worker configuration')
    if not all(isinstance(config[key], str) for key in required):
        raise ValueError('invalid worker configuration')
    host.render_unit('0' * 32, config['server_ipv4'])
    if config.get('mail_proxy_port') is not None:
        host.render_mail_proxy_unit('0' * 32, config['mail_proxy_port'])
    if not re.fullmatch(r'[0-9a-f]{64}', config['container_id']) or not re.fullmatch(r'sha256:[0-9a-f]{64}', config['image_id']):
        raise ValueError('immutable platform container and image IDs required')
    return config


def platform_command(config, args, execute=run):
    inspected = json.loads(execute(['/usr/bin/docker', 'inspect', 'asrobot'], timeout=8).stdout)
    if (len(inspected) != 1 or inspected[0].get('Id') != config['container_id'] or
            inspected[0].get('Image') != config['image_id'] or not inspected[0].get('State', {}).get('Running')):
        raise ValueError('platform container does not match the approved build')
    result = execute(['/usr/bin/docker', 'exec', config['container_id'], 'python', '-m', 'app.instance_admin', *args], timeout=8)
    return json.loads(result.stdout)


def preflight(instance_id, server_ipv4, execute=run):
    """Fail closed if the service's terminal sandbox or network guard fails."""
    account = host.account_name(instance_id)
    unit = host.render_unit(instance_id, server_ipv4)
    permitted = ('NoNewPrivileges=', 'PrivateTmp=', 'PrivateDevices=', 'ProtectHome=', 'ProtectSystem=',
                 'ReadWritePaths=', 'ProtectKernelModules=', 'ProtectControlGroups=', 'ProtectProc=',
                 'RestrictSUIDSGID=', 'CapabilityBoundingSet=', 'RestrictAddressFamilies=',
                 'SystemCallArchitectures=', 'IPAddressDeny=', 'IPAddressAllow=')
    properties = [line for line in unit.splitlines() if line.startswith(permitted)]
    script = '''import errno, socket, subprocess, sys
result = subprocess.run(['/usr/bin/bwrap','--unshare-user','--unshare-pid','--ro-bind','/','/','--proc','/proc','--dev','/dev','/bin/true'], timeout=10)
if result.returncode: sys.exit(10)
for ip, port in (('127.0.0.1', int(sys.argv[2])), (sys.argv[1], 22)):
    with socket.socket() as connection:
        connection.settimeout(2)
        try: connection.connect((ip, port))
        except (TimeoutError, socket.timeout): continue
        except OSError as exc:
            if exc.errno in (errno.EPERM, errno.EACCES): continue
            sys.exit(11)
        sys.exit(11)
'''
    args = ['/usr/bin/systemd-run', '--quiet', '--wait', '--collect', '--pipe', '--uid', account,
            '--unit', 'asrobot-preflight-' + instance_id]
    for prop in properties:
        args.extend(['--property', prop])
    # Use a private, live listener: a timeout inside the unit counts as blocked
    # only after the same endpoint has just accepted a host-side connection.
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        listener.listen(2)
        listener.settimeout(1)
        port = listener.getsockname()[1]
        with socket.create_connection(('127.0.0.1', port), timeout=1):
            probe, _ = listener.accept()
            probe.close()
        execute(args + ['/usr/bin/python3', '-c', script, server_ipv4, str(port)], timeout=40)
        listener.settimeout(0.2)
        try:
            unexpected, _ = listener.accept()
        except (TimeoutError, socket.timeout):
            pass
        else:
            unexpected.close()
            raise ValueError('instance can reach the host')


def process_pending(record, config, state_dir, execute=run, prepare=host.prepare_empty_service, sleep=time.sleep):
    if not isinstance(record, dict) or set(record) != {'instance_id', 'user_id'}:
        raise ValueError('invalid pending request')
    identity, user_id = record['instance_id'], record['user_id']
    if not isinstance(user_id, str) or not re.fullmatch(r'[0-9a-f]{32}', user_id):
        raise ValueError('invalid pending user')
    host.account_name(identity)
    state = state_dir / (identity + '.json')
    fd = os.open(state, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_NOFOLLOW', 0), 0o600)
    with os.fdopen(fd, 'w') as out:
        json.dump({'state': 'preparing'}, out)
    service = 'asrobot-instance-' + identity + '.service'
    started = False
    activated = False
    try:
        prepare(identity, config['server_ipv4'], config.get('mail_proxy_port'))
        preflight(identity, config['server_ipv4'], execute=execute)
        started = True
        execute(['/usr/bin/systemctl', 'enable', '--now', service], timeout=60)
        for attempt in range(8):
            try:
                result = platform_command(config, ['activate', user_id], execute=execute)
                if result != {'instance_id': identity, 'state': 'active'}:
                    raise ValueError('platform activated a different instance')
                activated = True
                break
            except (subprocess.CalledProcessError, subprocess.TimeoutExpired, json.JSONDecodeError):
                try:
                    current = platform_command(config, ['status', user_id], execute=execute)
                    if current == {'instance_id': identity, 'state': 'active'}:
                        activated = True
                        break
                except (subprocess.CalledProcessError, subprocess.TimeoutExpired, json.JSONDecodeError):
                    pass
                if attempt == 7:
                    raise
                sleep(1)
        # Replace only our own root-created record; no credentials are stored.
        temp = state_dir / (identity + '.complete')
        fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_NOFOLLOW', 0), 0o600)
        with os.fdopen(fd, 'w') as out:
            json.dump({'state': 'active'}, out)
        os.replace(temp, state)
    except BaseException:
        if started and not activated:
            execute(['/usr/bin/systemctl', 'disable', '--now', service], check=False, timeout=60)
        raise
    return {'state': 'active'}


def main():
    if os.geteuid() != 0:
        raise SystemExit('root required')
    config = read_config()
    directory = Path('/var/lib/asrobot-provision')
    directory.mkdir(mode=0o700, exist_ok=True)
    info = directory.lstat()
    if directory.is_symlink() or not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077:
        raise SystemExit('private worker state directory required')
    lock_fd = os.open(directory / 'worker.lock', os.O_RDWR | os.O_CREAT | getattr(os, 'O_NOFOLLOW', 0), 0o600)
    with os.fdopen(lock_fd, 'r+') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        pending = platform_command(config, ['list-pending'])
        if not isinstance(pending, list) or len(pending) > 256:
            raise ValueError('invalid platform provisioning queue')
        for record in pending:
            identity = record.get('instance_id') if isinstance(record, dict) else None
            if not isinstance(identity, str) or not re.fullmatch(r'[0-9a-f]{32}', identity):
                raise ValueError('invalid pending identity')
            if (directory / (identity + '.json')).exists():
                continue
            process_pending(record, config, directory)
            break


if __name__ == '__main__':
    main()
