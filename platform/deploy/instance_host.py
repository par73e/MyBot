"""Render a host-isolated assistant service; never installs or starts it."""
import ipaddress
import hashlib
import json
import os
import pwd
import re
import secrets
import stat
import subprocess
from pathlib import Path

PRIVATE_NETWORKS = ('0.0.0.0/8','10.0.0.0/8','127.0.0.0/8','169.254.0.0/16',
                    '172.16.0.0/12','192.168.0.0/16','100.64.0.0/10','::/0')
BLANK_CONFIG_SHA256 = '81b69cb5a994d72a46b98cc806988716497367c6ed6b9339efb0f6c1b400b334'
BLANK_PROMPT_SHA256 = '2a31a0dcb439ea9248f3c7a2ae61fe3fe51ea7cba1f5eeac2d5dd148409d2b5f'


def account_name(instance_id):
    if not isinstance(instance_id,str) or not re.fullmatch(r'[0-9a-f]{32}',instance_id):
        raise ValueError('invalid instance identifier')
    return 'asr-' + instance_id[:20]


def inspect_empty_scaffold(root, instance_id, expected_uid):
    """Verify an untouched prepared instance before an operator provisions it.

    This performs no host changes and does not authorize installing a service.
    Existing or migrated homes need a separate, data-preserving procedure.
    """
    account_name(instance_id)
    root = Path(root)
    if not root.is_absolute() or root.is_symlink():
        raise ValueError('invalid root')
    directory = root / instance_id
    expected_dirs = {'.', 'home', 'home/prompts', 'workspace', 'state'}
    expected_files = {'instance.json', 'native.token', 'home/config.toml', 'home/prompts/assistant.md'}
    seen_dirs, seen_files = set(), set()
    for parent, dirs, files in os.walk(directory, followlinks=False):
        p = Path(parent)
        for item in [p] + [p / name for name in dirs + files]:
            info = item.lstat()
            if info.st_uid != expected_uid or info.st_mode & 0o077:
                raise ValueError('instance files must be private and owned by the platform')
            if not (stat.S_ISDIR(info.st_mode) or (stat.S_ISREG(info.st_mode) and info.st_nlink == 1)):
                raise ValueError('unsupported file or link in scaffold')
        seen_dirs.add(p.relative_to(directory).as_posix())
        seen_files.update((p / name).relative_to(directory).as_posix() for name in files)
    if seen_dirs != expected_dirs or seen_files != expected_files:
        raise ValueError('scaffold is incomplete or contains extra files')
    if json.loads((directory / 'instance.json').read_text()) != {'version': 1, 'instance_id': instance_id, 'status': 'prepared', 'running': False}:
        raise ValueError('scaffold identity mismatch')
    if len((directory / 'native.token').read_text().strip()) < 32:
        raise ValueError('scaffold token is invalid')
    return directory


def render_unit(instance_id, public_ipv4, mail_proxy_port=None):
    """Return an operator-reviewable unit, with no user-supplied shell text."""
    account=account_name(instance_id)
    if mail_proxy_port is not None and (isinstance(mail_proxy_port, bool) or not isinstance(mail_proxy_port, int) or not 1 <= mail_proxy_port <= 65535):
        raise ValueError('invalid mail proxy port')
    ip=ipaddress.ip_address(public_ipv4)
    if ip.version != 4 or not ip.is_global:
        raise ValueError('public server IPv4 address required')
    root='/var/lib/asrobot-instances/'+instance_id
    unit='asrobot-instance-'+instance_id+'.service'
    env={
        'HOME':root, 'REASONIX_HOME':root+'/home', 'REASONIX_ASSISTANT':'1',
        'ASROBOT_INSTANCE_MODE':'isolated', 'ASROBOT_RUNTIME_ROOT':root,
        'ASROBOT_RUNTIME_HOME':root+'/home', 'ASROBOT_RUNTIME_WORKSPACE':root+'/workspace',
        'ASROBOT_RUNTIME_STATE':root+'/state', 'ASROBOT_RUNTIME_TOKEN':root+'/native.token',
        'ASROBOT_RUNTIME_SOCKET':root+'/native.sock', 'ASROBOT_RUNTIME_EXECUTABLE':'/opt/asrobot-agent/reasonix',
        'ASROBOT_RUNTIME_MAILCHECK':'/opt/asrobot-agent/mailcheck.py',
        'ASROBOT_BRIDGE_EXECUTABLE':'/opt/asrobot-agent/bridge', 'ASROBOT_RUNTIME_UNIT':unit,
        'ASROBOT_PLATFORM_UID':'10001', 'TZ':'Asia/Shanghai',
        'DO_NOT_TRACK':'1', 'REASONIX_TELEMETRY':'0',
    }
    if mail_proxy_port is not None:
        env['ASROBOT_MAIL_PROXY_SOCKET'] = root+'/state/mail-proxy.sock'
        env['ASROBOT_MAIL_PROXY_PORT'] = str(mail_proxy_port)
    lines=['[Unit]', 'Description=AS Robot isolated assistant instance '+instance_id,
           'After=network-online.target', 'Wants=network-online.target', '', '[Service]',
           'Type=simple', 'User='+account, 'Group='+account, 'WorkingDirectory='+root+'/workspace']
    if mail_proxy_port is not None:
        lines.insert(4, 'Wants=asrobot-mail-proxy-'+instance_id+'.service')
        lines.insert(4, 'After=asrobot-mail-proxy-'+instance_id+'.service')
    lines += ['Environment='+key+'='+value for key,value in env.items()]
    lines += ['ExecStart=/usr/bin/python3 /opt/asrobot-agent/instance_supervisor.py',
              'Restart=on-failure', 'RestartSec=5', 'UMask=0077', 'NoNewPrivileges=yes',
              'PrivateTmp=yes', 'PrivateDevices=yes', 'ProtectHome=yes', 'ProtectSystem=strict',
              'ReadWritePaths='+root, 'ProtectKernelModules=yes', 'ProtectControlGroups=yes',
              'ProtectProc=invisible', 'RestrictSUIDSGID=yes', 'CapabilityBoundingSet=',
              'RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK',
              'SystemCallArchitectures=native', 'TasksMax=128', 'MemoryMax=1G', 'CPUQuota=100%']
    lines += ['IPAddressDeny='+network for network in PRIVATE_NETWORKS]
    lines += ['IPAddressDeny='+str(ip)+'/32', 'IPAddressAllow=127.0.0.53/32', '',
              '[Install]', 'WantedBy=multi-user.target', '']
    return '\n'.join(lines)


def render_mail_proxy_unit(instance_id, upstream_port):
    account = account_name(instance_id)
    if isinstance(upstream_port, bool) or not isinstance(upstream_port, int) or not 1 <= upstream_port <= 65535:
        raise ValueError('invalid mail proxy port')
    root = '/var/lib/asrobot-instances/' + instance_id
    lines = ['[Unit]', 'Description=AS Robot private Gmail relay '+instance_id, 'After=network-online.target', '',
             '[Service]', 'Type=simple', 'User='+account, 'Group='+account,
             'Environment=ASROBOT_INSTANCE_MODE=isolated', 'Environment=ASROBOT_RUNTIME_ROOT='+root,
             'Environment=ASROBOT_RUNTIME_STATE='+root+'/state', 'Environment=ASROBOT_MAIL_PROXY_PORT='+str(upstream_port),
             'ExecStart=/usr/bin/python3 /opt/asrobot-agent/mail_proxy_relay.py',
             'Restart=on-failure', 'RestartSec=5', 'UMask=0077', 'NoNewPrivileges=yes',
             'PrivateTmp=yes', 'PrivateDevices=yes', 'ProtectHome=yes', 'ProtectSystem=strict',
             'ReadWritePaths='+root+'/state', 'ProtectKernelTunables=yes', 'ProtectKernelModules=yes',
             'ProtectControlGroups=yes', 'ProtectProc=invisible', 'RestrictSUIDSGID=yes', 'CapabilityBoundingSet=',
             'RestrictAddressFamilies=AF_UNIX AF_INET', 'SystemCallArchitectures=native',
             'TasksMax=32', 'MemoryMax=128M', 'CPUQuota=50%',
             'IPAddressDeny=0.0.0.0/0', 'IPAddressDeny=::/0', 'IPAddressAllow=127.0.0.1/32', '']
    return '\n'.join(lines)


def prepare_empty_service(instance_id, public_ipv4, mail_proxy_port=None):
    """Prepare one stopped host service from root-owned deployment templates.

    This operator-only primitive neither starts the assistant nor changes a
    platform account. Failed preparations remain stopped for manual review.
    It cannot import or overwrite an existing user's data.
    """
    if os.geteuid() != 0:
        raise PermissionError('root required')
    unit = render_unit(instance_id, public_ipv4, mail_proxy_port)
    proxy_unit = render_mail_proxy_unit(instance_id, mail_proxy_port) if mail_proxy_port is not None else None
    name = account_name(instance_id)
    root = Path('/var/lib/asrobot-instances')
    agent = Path('/opt/asrobot-agent')
    unit_path = Path('/etc/systemd/system') / ('asrobot-instance-' + instance_id + '.service')
    proxy_path = Path('/etc/systemd/system') / ('asrobot-mail-proxy-' + instance_id + '.service')
    for directory in (root, agent):
        info = directory.lstat()
        if not stat.S_ISDIR(info.st_mode) or directory.is_symlink() or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError('deployment directories must be root-owned and not writable by other users')
        if info.st_mode & 0o011 != 0o011:
            raise ValueError('deployment directories must be traversable by the isolated account')
    if (root / instance_id).exists() or (root / instance_id).is_symlink() or unit_path.exists() or unit_path.is_symlink():
        raise ValueError('instance or service already exists')
    if proxy_unit is not None and (proxy_path.exists() or proxy_path.is_symlink()):
        raise ValueError('mail relay service already exists')
    try:
        pwd.getpwnam(name)
    except KeyError:
        pass
    else:
        raise ValueError('instance operating-system account already exists')
    deployment_files = {}
    filenames = ['reasonix', 'bridge', 'instance_supervisor.py', 'mailcheck.py', 'mail_transport.py', 'blank-config.toml', 'blank-prompt.md']
    if proxy_unit is not None:
        filenames.append('mail_proxy_relay.py')
    for filename in filenames:
        path = agent / filename
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or path.is_symlink() or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError('deployment file is not protected')
        deployment_files[filename] = path
    config = deployment_files['blank-config.toml'].read_bytes()
    prompt = deployment_files['blank-prompt.md'].read_bytes()
    if (hashlib.sha256(config).hexdigest() != BLANK_CONFIG_SHA256 or
            hashlib.sha256(prompt).hexdigest() != BLANK_PROMPT_SHA256):
        raise ValueError('blank instance templates do not match the reviewed version')
    directory = root / instance_id
    directory.mkdir(mode=0o700)
    subprocess.run(['/usr/sbin/useradd', '--system', '--user-group', '--no-create-home',
                    '--home-dir', str(directory), '--shell', '/usr/sbin/nologin', name], check=True)
    user = pwd.getpwnam(name)
    for part in ('home', 'workspace', 'state', 'home/prompts'):
        (directory / part).mkdir(mode=0o700)
    manifest = json.dumps({'version': 1, 'instance_id': instance_id, 'status': 'prepared', 'running': False}).encode()
    files = {'native.token': secrets.token_urlsafe(48).encode(), 'instance.json': manifest,
             'home/config.toml': config, 'home/prompts/assistant.md': prompt}
    for relative, value in files.items():
        fd = os.open(directory / relative, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_NOFOLLOW', 0), 0o600)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(value)
    for parent, dirs, names in os.walk(directory, topdown=False, followlinks=False):
        for child in dirs + names:
            os.chown(Path(parent) / child, user.pw_uid, user.pw_gid, follow_symlinks=False)
    os.chown(directory, user.pw_uid, user.pw_gid, follow_symlinks=False)
    # The platform can traverse the instance and authenticate to its socket;
    # it cannot list or open the owner's workspace or account credentials.
    subprocess.run(['/usr/bin/setfacl', '-m', 'u:10001:--x', str(directory)], check=True)
    subprocess.run(['/usr/bin/setfacl', '-m', 'u:10001:r--', str(directory / 'native.token')], check=True)
    fd = os.open(unit_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_NOFOLLOW', 0), 0o644)
    with os.fdopen(fd, 'w') as stream:
        stream.write(unit)
    if proxy_unit is not None:
        fd = os.open(proxy_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_NOFOLLOW', 0), 0o644)
        with os.fdopen(fd, 'w') as stream:
            stream.write(proxy_unit)
    subprocess.run(['/usr/bin/systemctl', 'daemon-reload'], check=True)
    return {'instance_id': instance_id, 'service': unit_path.name, 'state': 'prepared-and-stopped'}


if __name__ == '__main__':
    import argparse
    parser = argparse.ArgumentParser(description='Prepare a stopped, empty isolated assistant service.')
    parser.add_argument('instance_id', help='already-reserved 32-character instance identifier')
    parser.add_argument('--server-ipv4', required=True, help='server public IPv4 to block from this instance')
    parser.add_argument('--mail-proxy-port', type=int, help='local proxy port for the private Gmail-only relay')
    arguments = parser.parse_args()
    print(json.dumps(prepare_empty_service(arguments.instance_id, arguments.server_ipv4, arguments.mail_proxy_port)))
