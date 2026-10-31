"""Offline database checkpoint and guarded recovery for instance cutover.

Only the operator runs this with all platform containers and both assistant
runtimes stopped. Recovery refuses a database that changed after the reviewed
post-cutover checkpoint; it cannot silently discard subsequent user writes.
"""
from contextlib import closing
import importlib.util
import os
from pathlib import Path
import pwd
import re
import sqlite3
import stat
import tempfile

base = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('instance_cutover', base/'instance_cutover.py')
cutover = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cutover)


def _private_file(path, owner=0):
    path = Path(path)
    info = path.lstat()
    if path.is_symlink() or not stat.S_ISREG(info.st_mode) or info.st_uid != owner or info.st_mode & 0o077:
        raise ValueError('private root-owned checkpoint required')
    return path


def _fence(identity, container_id, image_id, execute):
    cutover.host.account_name(identity)
    cutover._assert_stopped_old_services(execute)
    cutover._assert_platform_stopped(container_id, image_id, execute)
    unit = 'asrobot-instance-' + identity + '.service'
    if (execute(['/usr/bin/systemctl','is-active',unit], check=False).stdout.strip() != 'inactive' or
            execute(['/usr/bin/systemctl','is-enabled',unit], check=False).stdout.strip() != 'disabled'):
        raise ValueError('candidate instance must be stopped and disabled')
    account = pwd.getpwnam(cutover.host.account_name(identity))
    if not cutover.installer._no_processes(account.pw_uid):
        raise ValueError('candidate account still has running processes')


def _connect_readonly(path):
    return sqlite3.connect('file:'+str(path)+'?mode=ro', uri=True)


def _check(db):
    if db.execute('PRAGMA quick_check').fetchone()[0] != 'ok':
        raise ValueError('database integrity check failed')


def _equal_database(first, second):
    # A logical comparison includes schema, rows and SQLite sequences, while
    # ignoring harmless physical layout and journal differences.
    return tuple(first.iterdump()) == tuple(second.iterdump())


def checkpoint(identity, container_id, image_id, execute=cutover._run):
    if os.geteuid() != 0:
        raise PermissionError('root required')
    _fence(identity, container_id, image_id, execute)
    root = cutover.BACKUP_ROOT
    info = root.lstat()
    if root.is_symlink() or not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077:
        raise ValueError('private backup root required')
    source = _private_file(cutover.DATA_ROOT/'platform.sqlite3', owner=10001)
    directory = Path(tempfile.mkdtemp(prefix='instance-cutover-checkpoint-',dir=root))
    target = directory/'platform.sqlite3'
    with closing(_connect_readonly(source)) as db, closing(sqlite3.connect(str(target))) as saved:
        _check(db)
        db.backup(saved)
        _check(saved)
        if not _equal_database(db, saved):
            raise ValueError('checkpoint differs from platform database')
    target.chmod(0o600)
    _fence(identity, container_id, image_id, execute)
    return str(target)


def restore(identity, container_id, image_id, before, after, execute=cutover._run):
    if os.geteuid() != 0:
        raise PermissionError('root required')
    _fence(identity, container_id, image_id, execute)
    paths = []
    root = cutover.BACKUP_ROOT.resolve()
    for value in (before, after):
        path = _private_file(value)
        parent = path.parent
        info = parent.lstat()
        if (parent.is_symlink() or parent.parent.resolve() != root or
                not re.fullmatch(r'instance-cutover-checkpoint-[a-z0-9_]+',parent.name) or
                info.st_uid != 0 or info.st_mode & 0o077 or path.name != 'platform.sqlite3'):
            raise ValueError('invalid cutover checkpoint location')
        paths.append(path)
    if paths[0] == paths[1]:
        raise ValueError('distinct checkpoints required')
    target = _private_file(cutover.DATA_ROOT/'platform.sqlite3', owner=10001)
    with closing(_connect_readonly(paths[0])) as original, closing(_connect_readonly(paths[1])) as expected, closing(sqlite3.connect(str(target))) as current:
        for db in (original, expected, current): _check(db)
        if not _equal_database(current, expected):
            raise ValueError('platform database changed after checkpoint; manual reconciliation required')
        _fence(identity, container_id, image_id, execute)
        original.backup(current)
        _check(current)
        if not _equal_database(original, current):
            raise ValueError('restored database does not match checkpoint')
    return {'state':'restored','assistant_running':False}
