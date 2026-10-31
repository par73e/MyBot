"""Install a verified offline candidate into an empty, stopped host instance.

This is an operator-only primitive. It never stops legacy services, changes
platform ownership, starts the candidate, or sends messages. The cutover
coordinator must perform those actions with a fresh snapshot and rollback plan.
"""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import pwd
import shutil
import stat
import subprocess


def _load(name, filename):
    base = Path(__file__).resolve().parent
    path = base / filename
    if filename == 'migration_plan.py' and not path.exists() and base.name == 'deploy' and base.parent.name == 'platform':
        path = base.parent / 'app' / filename
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


host = _load('instance_host', 'instance_host.py')
migration = _load('migration_plan', 'migration_plan.py')
INSTANCE_ROOT = Path('/var/lib/asrobot-instances')
UNIT_ROOT = Path('/etc/systemd/system')


def _run(args, *, check=True):
    return subprocess.run(args, check=check, capture_output=True, text=True, timeout=30)


def _no_processes(uid):
    for entry in Path('/proc').iterdir():
        if not entry.name.isdigit():
            continue
        try:
            for line in (entry/'status').read_text().splitlines():
                if line.startswith('Uid:') and uid in map(int, line.split()[1:]):
                    return False
        except FileNotFoundError:
            continue
    return True


def _assert_unit_loaded(path, execute):
    fragment = execute(['/usr/bin/systemctl','show',path.name,'--property=FragmentPath','--value']).stdout.strip()
    stale = execute(['/usr/bin/systemctl','show',path.name,'--property=NeedDaemonReload','--value']).stdout.strip()
    if fragment != str(path) or stale != 'no':
        raise ValueError('systemd has not loaded the reviewed service')


def install_staged(snapshots, old_paths, candidate, identity, name, address, server_ipv4, execute=_run):
    if os.geteuid() != 0:
        raise PermissionError('root required')
    account = host.account_name(identity)
    planned = '/var/lib/asrobot-instances/' + identity
    # Verifies source, candidate, user-visible data, paths and WeChat ownership
    # before touching the prepared empty instance.
    verification = migration.verify_legacy_candidate(snapshots, old_paths, candidate, planned, name, address)
    proxy_port = verification['mail_proxy_port']
    expected_unit = host.render_unit(identity, server_ipv4, proxy_port)
    expected_proxy = host.render_mail_proxy_unit(identity, proxy_port) if proxy_port is not None else None
    for path, expected in ((UNIT_ROOT/('asrobot-instance-'+identity+'.service'), expected_unit),
                           (UNIT_ROOT/('asrobot-mail-proxy-'+identity+'.service'), expected_proxy)):
        if expected is None:
            if path.exists() or path.is_symlink():
                raise ValueError('unexpected mail relay service')
            continue
        info = path.lstat()
        if path.is_symlink() or not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022 or path.read_text() != expected:
            raise ValueError('prepared instance service differs')
        _assert_unit_loaded(path, execute)
    root = INSTANCE_ROOT
    root_info = root.lstat()
    if root.is_symlink() or not stat.S_ISDIR(root_info.st_mode) or root_info.st_uid != 0 or root_info.st_mode & 0o022:
        raise ValueError('instance root is not protected')
    target = root / identity
    info = target.lstat()
    user = pwd.getpwnam(account)
    if target.is_symlink() or not stat.S_ISDIR(info.st_mode) or info.st_uid != user.pw_uid or info.st_gid != user.pw_gid:
        raise ValueError('prepared instance ownership differs')
    if {p.name for p in target.iterdir()} != {'home','workspace','state','native.token','instance.json'}:
        raise ValueError('prepared instance is not empty')
    service = 'asrobot-instance-' + identity + '.service'
    active = execute(['/usr/bin/systemctl','is-active',service], check=False).stdout.strip()
    enabled = execute(['/usr/bin/systemctl','is-enabled',service], check=False).stdout.strip()
    if active != 'inactive' or enabled != 'disabled' or not _no_processes(user.pw_uid):
        raise ValueError('prepared instance is not stopped and disabled')
    if proxy_port is not None:
        proxy_service = 'asrobot-mail-proxy-' + identity + '.service'
        if (execute(['/usr/bin/systemctl','is-active',proxy_service], check=False).stdout.strip() != 'inactive' or
                execute(['/usr/bin/systemctl','is-enabled',proxy_service], check=False).stdout.strip() != 'static'):
            raise ValueError('mail relay is not stopped and disabled')
    expected_acls = {
        target: {'user::rwx','user:10001:--x','group::---','mask::--x','other::---'},
        target/'native.token': {'user::rw-','user:10001:r--','group::---','mask::r--','other::---'},
    }
    for path, expected in expected_acls.items():
        acl = {line for line in execute(['/usr/bin/getfacl','-cpn',str(path)]).stdout.splitlines() if line}
        if acl != expected:
            raise ValueError('prepared instance access controls differ')
    expected_dirs = {'.','home','home/prompts','workspace','state'}
    expected_files = {'instance.json','native.token','home/config.toml','home/prompts/assistant.md'}
    seen_dirs, seen_files = set(), set()
    for parent, dirs, files in os.walk(target, followlinks=False):
        current = Path(parent)
        seen_dirs.add(current.relative_to(target).as_posix())
        for name in dirs + files:
            path = current/name
            metadata = path.lstat()
            if metadata.st_uid != user.pw_uid or metadata.st_gid != user.pw_gid:
                raise ValueError('prepared instance ownership differs')
            if stat.S_ISDIR(metadata.st_mode):
                if metadata.st_mode & 0o077:
                    raise ValueError('prepared instance directory is not private')
            elif stat.S_ISREG(metadata.st_mode) and metadata.st_nlink == 1:
                if path != target/'native.token' and metadata.st_mode & 0o077:
                    raise ValueError('prepared instance file is not private')
                seen_files.add(path.relative_to(target).as_posix())
            else:
                raise ValueError('prepared instance contains a link or special file')
    if seen_dirs != expected_dirs or seen_files != expected_files:
        raise ValueError('prepared instance is not empty')
    if json.loads((target/'instance.json').read_text()) != {'version':1,'instance_id':identity,'status':'prepared','running':False}:
        raise ValueError('prepared instance manifest differs')
    if (hashlib.sha256((target/'home/config.toml').read_bytes()).hexdigest() != host.BLANK_CONFIG_SHA256 or
            hashlib.sha256((target/'home/prompts/assistant.md').read_bytes()).hexdigest() != host.BLANK_PROMPT_SHA256):
        raise ValueError('prepared instance templates differ')
    if len((target/'native.token').read_text().strip()) < 32:
        raise ValueError('prepared instance token is invalid')
    os.chown(target, 0, 0, follow_symlinks=False)
    os.chmod(target, 0o700)
    if not _no_processes(user.pw_uid):
        raise ValueError('instance process appeared during preparation')
    staging = target/'.migration-staging'
    staging.mkdir(mode=0o700)
    candidate = Path(candidate)
    for part in ('home','workspace','external','state'):
        shutil.copytree(candidate/part, staging/part, symlinks=False)
        migration.verify_private_copy(candidate/part, staging/part)
    for parent, dirs, files in os.walk(staging, topdown=False, followlinks=False):
        for name in dirs + files:
            os.chown(Path(parent)/name, user.pw_uid, user.pw_gid, follow_symlinks=False)
    for part in ('home','workspace','state'):
        os.rename(target/part, target/('.empty-'+part))
    for part in ('home','workspace','external','state'):
        os.rename(staging/part, target/part)
    if not _no_processes(user.pw_uid):
        raise ValueError('instance process appeared during preparation')
    os.chown(target, user.pw_uid, user.pw_gid, follow_symlinks=False)
    os.chmod(target, 0o700)
    execute(['/usr/bin/setfacl','-m','u:10001:--x',str(target)])
    return {'state':'installed-and-stopped','verification':verification}


if __name__ == '__main__':
    import sys
    raw = sys.stdin.buffer.read(65537)
    if len(raw) > 65536:
        raise SystemExit('request too large')
    request = json.loads(raw)
    if not isinstance(request,dict) or set(request) != {'snapshots','old_paths','candidate','identity','name','address','server_ipv4'}:
        raise SystemExit('invalid request')
    print(json.dumps(install_staged(**request), ensure_ascii=False))
