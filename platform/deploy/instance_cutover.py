"""Commit a prepared legacy migration while all old services are stopped.

This primitive does not stop or start services. The operator must first make
fresh offline snapshots, install and check the stopped candidate, stop and
disable the old bot and bridge, and stop the platform container. It creates a
private database backup before committing the ownership transaction.
"""
import http.client
import importlib.util
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import socket
import sqlite3
import stat
import subprocess
import tempfile
import time
import tomllib
from contextlib import closing


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


base = Path(__file__).resolve().parent
plan_path = base/'migration_plan.py'
if not plan_path.exists() and base.name == 'deploy' and base.parent.name == 'platform':
    plan_path = base.parent/'app/migration_plan.py'
plan = _load('migration_plan', plan_path)
host = _load('instance_host', base/'instance_host.py')
installer = _load('instance_migration_host', base/'instance_migration_host.py')
DATA_ROOT = Path('/var/lib/asrobot')
INSTANCE_ROOT = Path('/var/lib/asrobot-instances')
BACKUP_ROOT = Path('/var/backups/asrobot')
LEGACY_ROOT = Path('/var/lib/mybot')
UNIT_ROOT = Path('/etc/systemd/system')
AGENT_ROOT = Path('/opt/asrobot-agent')


def _run(args, *, check=True):
    return subprocess.run(args, check=check, capture_output=True, text=True, timeout=30)


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('localhost', timeout=5)
        self.path = str(path)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(5)
        self.sock.connect(self.path)


def _bridge_get(socket_path, token, endpoint):
    connection = UnixHTTP(socket_path)
    try:
        connection.request('GET', endpoint, headers={'Authorization':'Bearer '+token})
        response = connection.getresponse()
        body = response.read(65537)
        if response.status != 200 or len(body) > 65536:
            raise ValueError('candidate bridge is not ready')
        return json.loads(body)
    finally:
        connection.close()


def _probe_gmail_relay(root, port, uid, gid, execute):
    transport = AGENT_ROOT/'mail_transport.py'
    info = transport.lstat()
    if transport.is_symlink() or not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
        raise ValueError('mail transport is not protected')
    script = '''import importlib.util, os, sys, urllib.error
root, port, script = sys.argv[1:]
os.environ.update(ASROBOT_INSTANCE_MODE='isolated', ASROBOT_RUNTIME_ROOT=root, ASROBOT_RUNTIME_STATE=root+'/state', ASROBOT_MAIL_PROXY_SOCKET=root+'/state/mail-proxy.sock', ASROBOT_MAIL_PROXY_PORT=port)
spec=importlib.util.spec_from_file_location('mail_transport',script)
module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
try:
    response=module.gmail_opener({'proxy':'http://127.0.0.1:'+port}).open('https://gmail.googleapis.com/',timeout=15)
    status=response.status; response.close()
except urllib.error.HTTPError as error:
    status=error.code
if not 200 <= status < 500: sys.exit(1)
'''
    execute(['/usr/bin/setpriv','--reuid',str(uid),'--regid',str(gid),'--clear-groups','/usr/bin/python3','-c',script,str(root),str(port),str(transport)])


def _assert_stopped_old_services(execute):
    for service in ('mybot-heartbeat.service','asrobot-native.service','asrobot-history.service'):
        active = execute(['/usr/bin/systemctl','is-active',service], check=False).stdout.strip()
        enabled = execute(['/usr/bin/systemctl','is-enabled',service], check=False).stdout.strip()
        if active != 'inactive' or enabled != 'disabled':
            raise ValueError('legacy services must be stopped and disabled')
    if not installer._no_processes(pwd.getpwnam('mybot').pw_uid):
        raise ValueError('legacy account still has running processes')


def _assert_platform_stopped(container_id, image_id, execute):
    if not re.fullmatch(r'[0-9a-f]{64}', container_id) or not re.fullmatch(r'sha256:[0-9a-f]{64}', image_id):
        raise ValueError('approved platform container and image required')
    inspected = json.loads(execute(['/usr/bin/docker','inspect',container_id]).stdout)
    if (len(inspected) != 1 or inspected[0].get('Id') != container_id or
            inspected[0].get('Image') != image_id or inspected[0].get('State',{}).get('Running') is not False):
        raise ValueError('approved platform container must be stopped')
    ids = execute(['/usr/bin/docker','ps','-q']).stdout.splitlines()
    if ids:
        running = json.loads(execute(['/usr/bin/docker','inspect',*ids]).stdout)
        data_root = DATA_ROOT.resolve()
        for container in running:
            for mount in container.get('Mounts', []):
                source = mount.get('Source')
                if isinstance(source,str) and source.startswith('/'):
                    path = Path(source).resolve()
                    if path == data_root or data_root in path.parents or path in data_root.parents:
                        raise ValueError('a running container can access platform data')


def snapshot_stopped_legacy(container_id, image_id, execute=_run, max_bytes=256*1024*1024):
    """Copy current legacy files only after verified service quiescence."""
    if os.geteuid() != 0:
        raise PermissionError('root required')
    _assert_stopped_old_services(execute)
    _assert_platform_stopped(container_id, image_id, execute)
    backup_info = BACKUP_ROOT.lstat()
    if BACKUP_ROOT.is_symlink() or not stat.S_ISDIR(backup_info.st_mode) or backup_info.st_uid != 0 or backup_info.st_mode & 0o077:
        raise ValueError('private backup root required')
    uid = pwd.getpwnam('mybot').pw_uid
    paths = {'home':LEGACY_ROOT/'reasonix', 'workspace':LEGACY_ROOT/'workspace',
             'mail':LEGACY_ROOT/'mail', 'weather':LEGACY_ROOT/'weather'}
    snapshots = {}
    parent = Path(tempfile.mkdtemp(prefix='fresh-instance-snapshot-',dir=BACKUP_ROOT))
    for kind, root in paths.items():
        info = root.lstat()
        if root.is_symlink() or not stat.S_ISDIR(info.st_mode) or info.st_uid != uid or info.st_mode & 0o077:
            raise ValueError('legacy data root is not private')
        files, dirs, databases, total = [], [], set(), 0
        for current, children, names in os.walk(root, followlinks=False):
            for name in children + names:
                path = Path(current)/name
                metadata = path.lstat()
                if metadata.st_uid != uid:
                    raise ValueError('legacy data ownership differs')
                if stat.S_ISDIR(metadata.st_mode):
                    dirs.append(path)
                elif stat.S_ISREG(metadata.st_mode):
                    if path.parent == root and path.name == '.asrobot-offline-snapshot':
                        raise ValueError('legacy root contains reserved marker')
                    total += metadata.st_size
                    if total > max_bytes:
                        raise ValueError('legacy data exceeds snapshot limit')
                    with path.open('rb') as stream:
                        if stream.read(16) == b'SQLite format 3\x00':
                            databases.add(path)
                    files.append((path, metadata))
                else:
                    raise ValueError('legacy data contains links or special files')
        destination = parent/kind
        destination.mkdir(mode=0o700)
        for directory in dirs:
            (destination/directory.relative_to(root)).mkdir(mode=0o700, parents=True, exist_ok=True)
        for path, metadata in files:
            suffix = next((s for s in ('-wal','-shm','-journal') if path.name.endswith(s)), '')
            if suffix and path.with_name(path.name[:-len(suffix)]) in databases:
                continue
            out = destination/path.relative_to(root)
            if path in databases:
                with plan._open_offline_sqlite(path) as source_db, closing(sqlite3.connect(str(out))) as target_db:
                    source_db.backup(target_db)
                    if target_db.execute('PRAGMA quick_check').fetchone()[0] != 'ok':
                        raise ValueError('snapshot database integrity check failed')
                out.chmod(0o600)
            else:
                source_fd = os.open(path, os.O_RDONLY | getattr(os,'O_NOFOLLOW',0))
                try:
                    current = os.fstat(source_fd)
                    if not stat.S_ISREG(current.st_mode) or (current.st_dev,current.st_ino,current.st_size,current.st_mtime_ns) != (metadata.st_dev,metadata.st_ino,metadata.st_size,metadata.st_mtime_ns):
                        raise ValueError('legacy data changed during snapshot')
                    target_fd = os.open(out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, metadata.st_mode & 0o700)
                    with os.fdopen(source_fd,'rb') as source_file, os.fdopen(target_fd,'wb') as target_file:
                        source_fd = None
                        shutil.copyfileobj(source_file,target_file)
                        final = os.fstat(source_file.fileno())
                        if (final.st_size,final.st_mtime_ns) != (metadata.st_size,metadata.st_mtime_ns):
                            raise ValueError('legacy data changed during snapshot')
                finally:
                    if source_fd is not None:
                        os.close(source_fd)
        snapshots[kind] = destination
    _assert_stopped_old_services(execute)
    _assert_platform_stopped(container_id, image_id, execute)
    for kind in paths:
        plan.verify_private_copy(snapshots[kind],paths[kind],_candidate_uid=uid)
    _assert_stopped_old_services(execute)
    _assert_platform_stopped(container_id, image_id, execute)
    for destination in snapshots.values():
        fd = os.open(destination/'.asrobot-offline-snapshot', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        os.close(fd)
    return {'snapshot_directory':str(parent), 'snapshot_count':len(snapshots)}


def commit_offline(snapshots, old_paths, candidate, identity, user_id, name, address, server_ipv4, container_id, image_id, execute=_run, bridge_get=_bridge_get, proxy_probe=_probe_gmail_relay):
    if os.geteuid() != 0:
        raise PermissionError('root required')
    host.account_name(identity)
    if not isinstance(user_id,str) or not re.fullmatch(r'[0-9a-f]{32}', user_id):
        raise ValueError('invalid platform user')
    _assert_stopped_old_services(execute)
    _assert_platform_stopped(container_id, image_id, execute)
    now = time.time()
    for path in snapshots.values():
        marker = Path(path)/'.asrobot-offline-snapshot'
        age = now - marker.stat().st_mtime
        if age < 0 or age > 600:
            raise ValueError('a fresh offline snapshot is required')
    planned = '/var/lib/asrobot-instances/'+identity
    verification = plan.verify_legacy_candidate(snapshots,old_paths,candidate,planned,name,address)
    proxy_port = verification['mail_proxy_port']
    unit_paths = [(UNIT_ROOT/('asrobot-instance-'+identity+'.service'),host.render_unit(identity,server_ipv4,proxy_port))]
    if proxy_port is not None:
        unit_paths.append((UNIT_ROOT/('asrobot-mail-proxy-'+identity+'.service'),host.render_mail_proxy_unit(identity,proxy_port)))
    for path, expected in unit_paths:
        info = path.lstat()
        if path.is_symlink() or not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022 or path.read_text() != expected:
            raise ValueError('installed instance service differs')
        installer._assert_unit_loaded(path, execute)
    if execute(['/usr/bin/systemctl','is-active','asrobot-instance-'+identity+'.service'], check=False).stdout.strip() != 'active':
        raise ValueError('candidate bridge service is not active')
    if proxy_port is not None:
        if (execute(['/usr/bin/systemctl','is-active','asrobot-mail-proxy-'+identity+'.service'], check=False).stdout.strip() != 'active' or
                execute(['/usr/bin/systemctl','is-active','mybot-proxy.service'], check=False).stdout.strip() != 'active'):
            raise ValueError('required mail proxy is not active')
    legacy_uid = pwd.getpwnam('mybot').pw_uid
    for kind in ('home','workspace','mail','weather'):
        live = Path(old_paths[kind])
        if not live.is_absolute() or live.is_symlink() or LEGACY_ROOT not in live.parents:
            raise ValueError('legacy live directory requires review')
        plan.verify_private_copy(snapshots[kind], live, _candidate_uid=legacy_uid)
    _assert_stopped_old_services(execute)
    _assert_platform_stopped(container_id, image_id, execute)
    target = INSTANCE_ROOT/identity
    user = pwd.getpwnam(host.account_name(identity))
    info = target.lstat()
    if target.is_symlink() or not stat.S_ISDIR(info.st_mode) or info.st_uid != user.pw_uid or info.st_gid != user.pw_gid:
        raise ValueError('installed instance ownership differs')
    candidate = Path(candidate)
    for part in ('home','workspace','external'):
        plan.verify_private_copy(candidate/part, target/part, _candidate_uid=user.pw_uid)
    identity_path = target/'state/workspace-id'
    if identity_path.is_symlink() or identity_path.read_text() != old_paths['workspace']:
        raise ValueError('installed workspace identity differs')
    token_path = target/'native.token'
    if token_path.is_symlink() or not token_path.is_file():
        raise ValueError('candidate credential unavailable')
    token = token_path.read_text().strip()
    if len(token) < 32:
        raise ValueError('candidate credential unavailable')
    status = bridge_get(target/'native.sock',token,'/v1/status?source=friday_full')
    ownership = bridge_get(target/'native.sock',token,'/v1/ownership?source=friday_full')
    if status.get('running') is not False or status.get('desired_running') is not False or ownership.get('credential_configured') is not True:
        raise ValueError('candidate assistant must be stopped and configured')
    account = tomllib.loads((candidate/'home/config.toml').read_text())['bot']['weixin']['account_id']
    if not isinstance(account,str) or not account or ownership.get('account') != account:
        raise ValueError('candidate WeChat ownership differs')
    if proxy_port is not None:
        proxy_probe(target, proxy_port, user.pw_uid, user.pw_gid, execute)
    db_path = DATA_ROOT/'platform.sqlite3'
    db_info = db_path.lstat()
    if db_path.is_symlink() or not stat.S_ISREG(db_info.st_mode) or db_info.st_mode & 0o077:
        raise ValueError('platform database is not private')
    backup_info = BACKUP_ROOT.lstat()
    if BACKUP_ROOT.is_symlink() or not stat.S_ISDIR(backup_info.st_mode) or backup_info.st_uid != 0 or backup_info.st_mode & 0o077:
        raise ValueError('private backup root required')
    backup_dir = Path(tempfile.mkdtemp(prefix='before-instance-cutover-',dir=BACKUP_ROOT))
    backup = backup_dir/'platform.sqlite3'
    with closing(sqlite3.connect(str(db_path))) as db:
        if db.execute('PRAGMA quick_check').fetchone()[0] != 'ok':
            raise ValueError('platform database integrity check failed')
        with closing(sqlite3.connect(str(backup))) as saved:
            db.backup(saved)
            if saved.execute('PRAGMA quick_check').fetchone()[0] != 'ok':
                raise ValueError('platform database backup failed integrity check')
        backup.chmod(0o600)
        db.execute('PRAGMA foreign_keys=ON')
        try:
            db.execute('BEGIN IMMEDIATE')
            _assert_stopped_old_services(execute)
            _assert_platform_stopped(container_id, image_id, execute)
            db.execute("CREATE TABLE IF NOT EXISTS assistant_instances(id TEXT PRIMARY KEY,user_id TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,created INTEGER NOT NULL,state TEXT NOT NULL DEFAULT 'reserved' CHECK(state IN ('reserved','active')))")
            fields = [row[1] for row in db.execute('PRAGMA table_info(assistant_instances)')]
            if fields != ['id','user_id','created','state']:
                raise ValueError('instance table requires manual review')
            info = list(db.execute('PRAGMA table_info(assistant_instances)'))
            if info[0][5] != 1 or info[1][3] != 1:
                raise ValueError('instance identity constraints require review')
            db.execute('CREATE UNIQUE INDEX IF NOT EXISTS instance_user_unique ON assistant_instances(user_id)')
            owner = db.execute('SELECT status FROM users WHERE id=?',(user_id,)).fetchone()
            workspace = db.execute('SELECT legacy_source,native_source FROM workspaces WHERE user_id=?',(user_id,)).fetchone()
            if owner != ('active',) or workspace != ('friday_owner','friday_full'):
                raise ValueError('legacy workspace ownership changed')
            if db.execute('SELECT 1 FROM assistant_instances WHERE user_id=? OR id=?',(user_id,identity)).fetchone():
                raise ValueError('instance identity already exists')
            if db.execute('SELECT 1 FROM bindings WHERE user_id=? OR external_id=?',(user_id,account)).fetchone():
                raise ValueError('WeChat binding needs reconciliation')
            db.execute("INSERT INTO assistant_instances(id,user_id,created,state) VALUES(?,?,?,'active')",(identity,user_id,int(now)))
            db.execute("INSERT INTO bindings(user_id,external_id,status) VALUES(?,?,'bound')",(user_id,account))
            db.execute('UPDATE workspaces SET legacy_source=NULL,native_source=NULL WHERE user_id=?',(user_id,))
            db.commit()
        except BaseException:
            db.rollback()
            raise
    return {'state':'ownership-committed','backup':str(backup),'assistant_running':False,'verification':verification}


if __name__ == '__main__':
    import sys
    if len(sys.argv) != 2 or sys.argv[1] not in ('snapshot','commit'):
        raise SystemExit('usage: instance_cutover.py snapshot|commit < request.json')
    raw = sys.stdin.buffer.read(65537)
    if len(raw) > 65536:
        raise SystemExit('request too large')
    request = json.loads(raw)
    snapshot_fields = {'container_id','image_id'}
    commit_fields = {'snapshots','old_paths','candidate','identity','user_id','name','address','server_ipv4','container_id','image_id'}
    if not isinstance(request,dict) or set(request) != (snapshot_fields if sys.argv[1] == 'snapshot' else commit_fields):
        raise SystemExit('invalid request')
    result = snapshot_stopped_legacy(**request) if sys.argv[1] == 'snapshot' else commit_offline(**request)
    print(json.dumps(result, ensure_ascii=False))
