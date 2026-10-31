"""Pure transformations for an offline assistant-instance migration.

No source file is opened, no scheduler is started, and no messages are sent.
The caller is responsible for staging a consistent copy and verifying ownership.
"""
from copy import deepcopy
from pathlib import PurePosixPath
from contextlib import contextmanager


@contextmanager
def _open_offline_sqlite(path):
    """Read a quiescent SQLite database without creating sidecars at source."""
    import os
    from pathlib import Path
    import shutil
    import sqlite3
    import stat
    import tempfile
    from urllib.parse import quote

    path = Path(path)
    has_journal = any(Path(str(path) + suffix).exists() for suffix in ('-wal', '-journal'))
    if not has_journal:
        connection = sqlite3.connect('file:' + quote(str(path), safe='/') + '?mode=ro&immutable=1', uri=True)
        try:
            yield connection
        finally:
            connection.close()
        return
    # SQLite may otherwise create or change -shm beside the original. Copy the
    # database and sidecars into an automatically removed private scratch dir.
    with tempfile.TemporaryDirectory(prefix='asrobot-sqlite-check-') as directory:
        copied = Path(directory) / 'database.sqlite3'
        for suffix in ('', '-wal', '-shm', '-journal'):
            original = Path(str(path) + suffix)
            if suffix and not original.exists() and not original.is_symlink():
                continue
            fd = os.open(original, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0))
            try:
                before = os.fstat(fd)
                if not stat.S_ISREG(before.st_mode):
                    raise ValueError('unsupported SQLite file')
                target = Path(str(copied) + suffix)
                output_fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                with os.fdopen(fd, 'rb') as source, os.fdopen(output_fd, 'wb') as output:
                    fd = None
                    shutil.copyfileobj(source, output)
                    after = os.fstat(source.fileno())
                    if (before.st_size, before.st_mtime_ns) != (after.st_size, after.st_mtime_ns):
                        raise ValueError('SQLite file changed during verification')
            finally:
                if fd is not None:
                    os.close(fd)
        connection = sqlite3.connect(str(copied))
        try:
            yield connection
        finally:
            connection.close()


def _absolute_workspace(value):
    if not isinstance(value, str) or not value.startswith('/') or str(PurePosixPath(value)) != value or '..' in PurePosixPath(value).parts or value == '/':
        raise ValueError('workspace must be a normalized absolute path')
    return value


def remap_heartbeat_workspace(document, old_workspace, new_workspace):
    """Return a staged copy with only the owned task workspace paths remapped.

    All notification targets, scheduling and delivery history remain unchanged.
    Unknown schemas and unrelated task roots require manual reconciliation.
    """
    old_workspace = _absolute_workspace(old_workspace)
    new_workspace = _absolute_workspace(new_workspace)
    if not isinstance(document, dict) or document.get('schemaVersion') != 1 or not isinstance(document.get('tasks'), list):
        raise ValueError('unsupported heartbeat document')
    output = deepcopy(document)
    seen = set()
    for task in output['tasks']:
        if not isinstance(task, dict) or not isinstance(task.get('id'), str) or not task['id'] or task['id'] in seen:
            raise ValueError('invalid or duplicate task identity')
        seen.add(task['id'])
        if task.get('workspaceRoot') not in (old_workspace, new_workspace):
            raise ValueError('task workspace requires manual reconciliation')
        task['workspaceRoot'] = new_workspace
    return output


def remap_staged_sqlite_jobs(connection, old_workspace, new_workspace):
    """Remap jobs in an isolated SQLite copy, preserving IDs and delivery state.

    The caller must supply a disposable, offline staging database. This function
    does not open paths and must never be used directly on a live database.
    """
    import json
    if connection.in_transaction:
        raise ValueError('staging connection must not have an active transaction')
    _absolute_workspace(old_workspace)
    _absolute_workspace(new_workspace)
    try:
        connection.execute('BEGIN IMMEDIATE')
        rows = connection.execute("SELECT id,payload FROM assistant_entities WHERE kind='job'").fetchall()
        tasks = []
        for row_id, raw in rows:
            task = json.loads(raw)
            if not isinstance(task, dict) or task.get('id') != row_id:
                raise ValueError('job identity mismatch')
            tasks.append(task)
        result = remap_heartbeat_workspace({'schemaVersion': 1, 'tasks': tasks}, old_workspace, new_workspace)
        updated = 0
        for (row_id, original), task in zip(rows, result['tasks']):
            payload = json.dumps(task, ensure_ascii=False, separators=(',', ':'))
            if json.loads(original) != task:
                changed = connection.execute("UPDATE assistant_entities SET payload=? WHERE kind='job' AND id=? AND payload=?", (payload, row_id, original)).rowcount
                if changed != 1:
                    raise ValueError('job changed during staging')
                updated += 1
        connection.commit()
        return {'examined': len(rows), 'updated': updated}
    except BaseException:
        connection.rollback()
        raise


def stage_offline_snapshot(source, destination, old_workspace, new_workspace, max_bytes=256*1024*1024, remap_jobs=True):
    """Copy an explicitly marked offline snapshot to a new private directory.

    This prepares a candidate only; it never starts a runtime or alters source.
    The marker documents operator intent but cannot itself prove quiescence.
    """
    import os
    import shutil
    import sqlite3
    import stat
    from contextlib import closing
    from pathlib import Path
    from urllib.parse import quote

    source, destination = Path(source), Path(destination)
    if not source.is_absolute() or not destination.is_absolute() or source.is_symlink() or not source.is_dir():
        raise ValueError('absolute, non-symbolic snapshot source required')
    source_stat = source.stat()
    if source_stat.st_uid != os.getuid() or source_stat.st_mode & 0o077:
        raise ValueError('snapshot source must be private and owned by the operator')
    marker = source / '.asrobot-offline-snapshot'
    if not marker.is_file() or marker.is_symlink():
        raise ValueError('explicit offline snapshot marker required')
    if destination.exists() or destination.is_symlink():
        raise ValueError('staging destination must not exist')
    source_real = source.resolve()
    parent_real = destination.parent.resolve(strict=True)
    if parent_real == source_real or source_real in parent_real.parents:
        raise ValueError('staging destination cannot be inside source')
    _absolute_workspace(old_workspace)
    _absolute_workspace(new_workspace)
    files, directories = [], []
    databases = set()
    total = 0
    for directory, subdirs, names in os.walk(source, followlinks=False):
        for name in sorted(subdirs + names):
            path = Path(directory) / name
            metadata = path.lstat()
            if not (stat.S_ISDIR(metadata.st_mode) or stat.S_ISREG(metadata.st_mode)):
                raise ValueError('snapshot contains unsupported file or symbolic link')
            if stat.S_ISDIR(metadata.st_mode):
                directories.append(path)
            if stat.S_ISREG(metadata.st_mode):
                total += metadata.st_size
                if total > max_bytes:
                    raise ValueError('snapshot exceeds staging limit')
                with path.open('rb') as stream:
                    if stream.read(16) == b'SQLite format 3\x00':
                        databases.add(path)
                files.append((path, metadata))
    destination.mkdir(mode=0o700)
    for directory in directories:
        (destination / directory.relative_to(source)).mkdir(mode=0o700, parents=True, exist_ok=True)
    copied, sqlite_count = 0, 0
    for path, metadata in files:
        rel = path.relative_to(source)
        if str(rel) == '.asrobot-offline-snapshot':
            continue
        suffix = next((suffix for suffix in ('-wal', '-shm', '-journal') if path.name.endswith(suffix)), '')
        if suffix and path.with_name(path.name[:-len(suffix)]) in databases:
            continue
        target = destination / rel
        target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        if path in databases:
            has_journal = any(path.with_name(path.name + suffix).exists() for suffix in ('-wal', '-journal'))
            uri = 'file:' + quote(str(path), safe='/') + ('?mode=ro' if has_journal else '?mode=ro&immutable=1')
            with closing(sqlite3.connect(uri, uri=True)) as old_db, closing(sqlite3.connect(target)) as new_db:
                old_db.backup(new_db)
            target.chmod(0o600)
            sqlite_count += 1
        else:
            flags = os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0)
            source_fd = os.open(path, flags)
            try:
                current = os.fstat(source_fd)
                if not stat.S_ISREG(current.st_mode) or (current.st_dev, current.st_ino) != (metadata.st_dev, metadata.st_ino):
                    raise ValueError('snapshot changed during staging')
                dest_fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, metadata.st_mode & 0o700)
                with os.fdopen(source_fd, 'rb') as source_file, os.fdopen(dest_fd, 'wb') as target_file:
                    source_fd = None
                    shutil.copyfileobj(source_file, target_file)
            finally:
                if source_fd is not None:
                    os.close(source_fd)
        copied += 1
    jobs = {'examined': 0, 'updated': 0}
    db_path = destination / 'assistant' / 'state.sqlite3'
    if remap_jobs and db_path.exists():
        with closing(sqlite3.connect(db_path)) as connection:
            exists = connection.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='assistant_entities'").fetchone()
            if exists:
                jobs = remap_staged_sqlite_jobs(connection, old_workspace, new_workspace)
    marker_fd = os.open(destination / '.asrobot-migration-candidate', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    os.close(marker_fd)
    # File copying and job-root remapping cannot verify provider credentials,
    # absolute configuration paths, session attachments or external delivery.
    # The staged result is never an activation approval.
    return {'files': copied, 'sqlite_databases': sqlite_count, 'jobs': jobs, 'requires_review': True}


def set_staged_identity(candidate, name, address):
    """Add a chosen name to a private candidate, never overwrite a profile."""
    import json
    import os
    import stat
    import unicodedata
    from pathlib import Path

    candidate = Path(candidate)
    if not candidate.is_absolute() or candidate.is_symlink():
        raise ValueError('private candidate required')
    metadata = candidate.lstat()
    marker = candidate / '.asrobot-migration-candidate'
    if (not stat.S_ISDIR(metadata.st_mode) or metadata.st_uid != os.getuid() or metadata.st_mode & 0o077 or
            not marker.is_file() or marker.is_symlink()):
        raise ValueError('private candidate marker required')
    if not isinstance(name, str) or not isinstance(address, str):
        raise ValueError('invalid assistant identity')
    name, address = name.strip(), address.strip()
    if not name or len(name) > 32 or len(address) > 32 or any(unicodedata.category(ch) == 'Cc' for ch in name + address):
        raise ValueError('invalid assistant identity')
    folder = candidate / 'assistant'
    if folder.is_symlink() or not folder.is_dir():
        raise ValueError('candidate assistant directory missing')
    data = json.dumps({'name': name, 'address': address}, ensure_ascii=False).encode()
    fd = os.open(folder / 'profile.json', os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_NOFOLLOW', 0), 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(data)


def verify_staged_snapshot(source, candidate, old_workspace, new_workspace):
    """Compare an offline database and conversation files without exposing data.

    Only the explicitly reviewed job workspace rewrite is permitted. The
    operator must still verify credentials, delivery ownership and quiescence.
    """
    import hashlib
    import json
    import os
    import sqlite3
    import stat
    from collections import Counter
    from contextlib import closing
    from pathlib import Path
    from urllib.parse import quote

    source, candidate = Path(source), Path(candidate)
    for root, marker in ((source, '.asrobot-offline-snapshot'), (candidate, '.asrobot-migration-candidate')):
        if not root.is_absolute() or root.is_symlink():
            raise ValueError('private snapshot paths required')
        info = root.lstat()
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077 or
                not (root / marker).is_file() or (root / marker).is_symlink()):
            raise ValueError('private snapshot marker required')
    _absolute_workspace(old_workspace)
    _absolute_workspace(new_workspace)
    databases = [root / 'assistant' / 'state.sqlite3' for root in (source, candidate)]
    for path in databases:
        if path.is_symlink() or not path.is_file():
            raise ValueError('assistant database is missing')
    def compare_databases(old_path, new_path, allow_job_rewrite):
        with _open_offline_sqlite(old_path) as original, _open_offline_sqlite(new_path) as migrated:
            for db in (original, migrated):
                if db.execute('PRAGMA quick_check').fetchone()[0] != 'ok':
                    raise ValueError('assistant database integrity check failed')
            schema = lambda db: list(db.execute("SELECT type,name,tbl_name,sql FROM sqlite_master ORDER BY type,name"))
            if schema(original) != schema(migrated):
                raise ValueError('assistant database schema differs')
            tables = lambda db: {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
            names = tables(original)
            if names != tables(migrated):
                raise ValueError('assistant database schema differs')
            rows_checked = 0
            for name in sorted(names):
                quoted = '"' + name.replace('"', '""') + '"'
                columns = lambda db: [row[1] for row in db.execute('PRAGMA table_info(' + quoted + ')')]
                fields = columns(original)
                if fields != columns(migrated):
                    raise ValueError('assistant database columns differ')
                def rows(db, rewrite):
                    values = Counter()
                    for row in db.execute('SELECT * FROM ' + quoted):
                        row = list(row)
                        if allow_job_rewrite and name == 'assistant_entities' and {'kind', 'id', 'payload'}.issubset(fields):
                            kind, ident, payload = (fields.index(x) for x in ('kind', 'id', 'payload'))
                            if row[kind] == 'job':
                                task = json.loads(row[payload])
                                if task.get('id') != row[ident]:
                                    raise ValueError('job identity mismatch')
                                if rewrite:
                                    task = remap_heartbeat_workspace({'schemaVersion': 1, 'tasks': [task]}, old_workspace, new_workspace)['tasks'][0]
                                row[payload] = json.dumps(task, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
                        values[tuple(row)] += 1
                    return values
                before, after = rows(original, True), rows(migrated, False)
                if before != after:
                    raise ValueError('assistant database content differs')
                rows_checked += sum(before.values())
            return rows_checked
    rows_checked = compare_databases(databases[0], databases[1], True)
    def session_files(root):
        base = root / 'projects'
        if not base.exists():
            return {}, set()
        if base.is_symlink() or not base.is_dir():
            raise ValueError('unsupported conversation directory')
        result, sqlite_paths, files_seen = {}, set(), []
        for parent, dirs, files in os.walk(base, followlinks=False):
            for name in dirs + files:
                path = Path(parent) / name
                info = path.lstat()
                if stat.S_ISDIR(info.st_mode):
                    continue
                if not stat.S_ISREG(info.st_mode):
                    raise ValueError('unsupported conversation file')
                files_seen.append(path)
                with path.open('rb') as stream:
                    if stream.read(16) == b'SQLite format 3\x00':
                        sqlite_paths.add(str(path.relative_to(base)))
        for path in files_seen:
            relative = str(path.relative_to(base))
            if any(relative.endswith(suffix) and relative[:-len(suffix)] in sqlite_paths for suffix in ('-wal', '-shm', '-journal')):
                continue
            if relative in sqlite_paths:
                result[relative] = None
                continue
            digest = hashlib.sha256()
            with path.open('rb') as stream:
                for block in iter(lambda: stream.read(1024 * 1024), b''):
                    digest.update(block)
            result[relative] = digest.digest()
        return result, sqlite_paths
    original_files, original_dbs = session_files(source)
    migrated_files, migrated_dbs = session_files(candidate)
    if original_files != migrated_files or original_dbs != migrated_dbs:
        raise ValueError('conversation files differ')
    for relative in original_dbs:
        rows_checked += compare_databases(source / 'projects' / relative, candidate / 'projects' / relative, False)
    return {'database_rows': rows_checked, 'conversation_files': len(original_files) - len(original_dbs), 'requires_credential_and_delivery_review': True}


def verify_private_copy(source, candidate, *, _excluded=frozenset(), _candidate_uid=None):
    """Compare copied external credential trees, returning counts only."""
    import hashlib
    import os
    import sqlite3
    import stat
    from collections import Counter
    from pathlib import Path

    allowed_exclusions = {'assistant/state.sqlite3', 'assistant/mail.json', 'assistant/weather.json', 'assistant/profile.json', 'skills/follow-builders/SKILL.md'}
    if not set(_excluded).issubset(allowed_exclusions):
        raise ValueError('unsupported verification exclusion')

    if _candidate_uid is not None and (os.getuid() != 0 or not isinstance(_candidate_uid, int) or _candidate_uid < 1):
        raise ValueError('candidate owner override requires the root operator')
    def inventory(root, owner):
        root = Path(root)
        if not root.is_absolute() or root.is_symlink():
            raise ValueError('private copy path required')
        info = root.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != owner or info.st_mode & 0o077:
            raise ValueError('private copy directory required')
        values, sqlite_paths, files_seen = {}, set(), []
        for parent, dirs, files in os.walk(root, followlinks=False):
            for name in dirs + files:
                path = Path(parent) / name
                item = path.lstat()
                if stat.S_ISDIR(item.st_mode):
                    if item.st_uid != owner or item.st_mode & 0o500 != 0o500:
                        raise ValueError('private copy directory ownership differs')
                    continue
                if not stat.S_ISREG(item.st_mode):
                    raise ValueError('unsupported file in private copy')
                if item.st_uid != owner or not item.st_mode & 0o400:
                    raise ValueError('private copy file ownership differs')
                if path.parent == root and path.name in ('.asrobot-offline-snapshot', '.asrobot-migration-candidate'):
                    continue
                relative = str(path.relative_to(root))
                if relative in _excluded or any(relative == item + suffix for item in _excluded for suffix in ('-wal', '-shm', '-journal')):
                    continue
                files_seen.append(path)
                with path.open('rb') as stream:
                    if stream.read(16) == b'SQLite format 3\x00':
                        sqlite_paths.add(str(path.relative_to(root)))
        for path in files_seen:
            relative = str(path.relative_to(root))
            if any(relative.endswith(suffix) and relative[:-len(suffix)] in sqlite_paths for suffix in ('-wal', '-shm', '-journal')):
                continue
            if relative in sqlite_paths:
                values[relative] = None
                continue
            digest = hashlib.sha256()
            with path.open('rb') as stream:
                for block in iter(lambda: stream.read(1024 * 1024), b''):
                    digest.update(block)
            values[relative] = digest.digest()
        return values, sqlite_paths
    original, original_dbs = inventory(source, os.getuid())
    copied, copied_dbs = inventory(candidate, os.getuid() if _candidate_uid is None else _candidate_uid)
    if original != copied or original_dbs != copied_dbs:
        raise ValueError('private copy does not match its source')
    for relative in original_dbs:
        with _open_offline_sqlite(Path(source) / relative) as old, _open_offline_sqlite(Path(candidate) / relative) as new:
            for db in (old, new):
                if db.execute('PRAGMA quick_check').fetchone()[0] != 'ok':
                    raise ValueError('private database integrity check failed')
            schema = lambda db: list(db.execute("SELECT type,name,tbl_name,sql FROM sqlite_master ORDER BY type,name"))
            if schema(old) != schema(new):
                raise ValueError('private database schema differs')
            names = [row[0] for row in old.execute("SELECT name FROM sqlite_master WHERE type='table'")]
            for name in names:
                query = 'SELECT * FROM "' + name.replace('"', '""') + '"'
                if Counter(old.execute(query)) != Counter(new.execute(query)):
                    raise ValueError('private database content differs')
    return {'verified_files': len(original) - len(original_dbs), 'verified_databases': len(original_dbs)}


def verify_staged_wechat(source, candidate):
    """Compare one saved WeChat owner and credential without returning them."""
    import hmac
    import json
    import os
    import re
    import stat
    from pathlib import Path
    try:
        import tomllib
    except ImportError:
        import tomli as tomllib

    values = []
    for root, marker in ((Path(source), '.asrobot-offline-snapshot'), (Path(candidate), '.asrobot-migration-candidate')):
        if not root.is_absolute() or root.is_symlink():
            raise ValueError('private snapshot path required')
        info = root.lstat()
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077 or
                not (root / marker).is_file() or (root / marker).is_symlink()):
            raise ValueError('private snapshot marker required')
        config_path = root / 'config.toml'
        if config_path.is_symlink() or not config_path.is_file():
            raise ValueError('WeChat configuration requires review')
        config = tomllib.loads(config_path.read_text())
        bot = config.get('bot', {})
        wechat = bot.get('weixin', {})
        owners = bot.get('allowlist', {}).get('weixin_users', [])
        account_id = wechat.get('account_id', '')
        if (not wechat.get('enabled') or not isinstance(owners, list) or len(owners) != 1 or
                not isinstance(owners[0], str) or not owners[0] or not isinstance(account_id, str) or
                not account_id or account_id in ('.', '..') or '/' in account_id or '\\' in account_id):
            raise ValueError('WeChat ownership requires review')
        path = root / 'weixin' / 'accounts' / (account_id + '.json')
        if path.is_symlink() or not path.is_file():
            raise ValueError('WeChat credential requires review')
        account = json.loads(path.read_text())
        token = account.get('token')
        if not isinstance(token, str) or not token or account.get('user_id') != owners[0]:
            raise ValueError('WeChat ownership requires review')
        # A configured environment token takes priority over the saved file.
        # Verify its original file byte-for-byte and require a manual decision
        # when such an override exists instead of guessing which identity won.
        token_env = wechat.get('token_env', 'WEIXIN_BOT_TOKEN')
        env_path = root / '.env'
        env_data = env_path.read_bytes() if env_path.is_file() and not env_path.is_symlink() else b''
        if env_path.is_symlink():
            raise ValueError('WeChat credential requires review')
        if not isinstance(token_env, str) or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', token_env):
            raise ValueError('WeChat credential requires review')
        pattern = re.compile(rb'^(?:export\s+)?' + re.escape(token_env.encode()) + rb'\s*=')
        if any(pattern.match(line.strip()) for line in env_data.splitlines()):
            raise ValueError('WeChat environment override requires manual review')
        api_base, saved_base = wechat.get('api_base', ''), account.get('base_url', '')
        if not isinstance(api_base, str) or not isinstance(saved_base, str):
            raise ValueError('WeChat configuration requires review')
        values.append(tuple(value.encode() for value in (account_id, owners[0], token, api_base, saved_base)) + (env_data,))
    if any(not hmac.compare_digest(a, b) for a, b in zip(values[0], values[1])):
        raise ValueError('WeChat configuration differs')
    return {'single_owner_matches': True, 'credential_matches': True, 'connection_tested': False}


def staged_gmail_proxy_port(candidate):
    """Identify one supported local Gmail proxy without returning credentials."""
    import json
    import os
    import stat
    from pathlib import Path
    from urllib.parse import urlsplit

    root = Path(candidate)
    if not root.is_absolute() or root.is_symlink():
        raise ValueError('private candidate required')
    info = root.lstat()
    if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077 or
            not (root/'.asrobot-migration-candidate').is_file() or (root/'.asrobot-migration-candidate').is_symlink()):
        raise ValueError('private candidate required')
    path = root/'assistant/mail.json'
    if (root/'assistant').is_symlink() or path.is_symlink():
        raise ValueError('mail configuration requires review')
    if not path.exists():
        return None
    config = json.loads(path.read_text())
    accounts = config.get('accounts', [])
    if not isinstance(accounts, list):
        raise ValueError('mail configuration requires review')
    ports = set()
    for account in accounts:
        if not isinstance(account, dict):
            raise ValueError('mail configuration requires review')
        proxy = account.get('proxy') or ''
        if account.get('provider') != 'gmail' or not proxy:
            continue
        if not isinstance(proxy, str):
            raise ValueError('mail proxy requires review')
        parsed = urlsplit(proxy)
        if parsed.hostname in ('127.0.0.1', 'localhost', '::1'):
            if (parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or parsed.username or parsed.password or
                    parsed.path not in ('', '/') or parsed.query or parsed.fragment or not parsed.port):
                raise ValueError('local mail proxy requires review')
            ports.add(parsed.port)
    if len(ports) > 1:
        raise ValueError('multiple local mail proxies require review')
    return next(iter(ports)) if ports else None


def remap_credential_config(document, kind, mappings):
    """Rewrite only known credential/location path fields in a staged config."""
    if not isinstance(document, dict) or kind not in ('mail', 'weather'):
        raise ValueError('unsupported credential configuration')
    normalized = [(_absolute_workspace(old), _absolute_workspace(new)) for old, new in mappings]
    normalized.sort(key=lambda pair: len(pair[0]), reverse=True)
    result = deepcopy(document)
    def remap(value):
        if value in ('', None):
            return value
        value = _absolute_workspace(value)
        for old, new in normalized:
            if value == old or value.startswith(old + '/'):
                return new + value[len(old):]
        raise ValueError('credential path needs manual reconciliation')
    if kind == 'mail':
        accounts = result.get('accounts', [])
        if not isinstance(accounts, list):
            raise ValueError('invalid mailbox configuration')
        for account in accounts:
            if not isinstance(account, dict):
                raise ValueError('invalid mailbox configuration')
            if 'credential_file' in account:
                account['credential_file'] = remap(account['credential_file'])
    else:
        for key in ('credential_file', 'locations_file'):
            if key in result:
                result[key] = remap(result[key])
    return result


def rewrite_staged_credential_configs(source, candidate, instance_root, mappings):
    """Idempotently rewrite only reviewed external paths in a staged copy."""
    import json
    import os
    import stat
    import tempfile
    from pathlib import Path

    source, candidate = Path(source), Path(candidate)
    _absolute_workspace(instance_root)
    for root, marker in ((source, '.asrobot-offline-snapshot'), (candidate, '.asrobot-migration-candidate')):
        if not root.is_absolute() or root.is_symlink():
            raise ValueError('private snapshot path required')
        info = root.lstat()
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077 or
                not (root / marker).is_file() or (root / marker).is_symlink()):
            raise ValueError('private snapshot marker required')
    seen = set()
    for old, new in mappings:
        _absolute_workspace(old)
        _absolute_workspace(new)
        if old in seen:
            raise ValueError('duplicate credential source mapping')
        seen.add(old)
        if new != instance_root and not new.startswith(instance_root + '/'):
            raise ValueError('credential destination must remain inside the instance')
    if (source / 'assistant').is_symlink() or (candidate / 'assistant').is_symlink():
        raise ValueError('credential directory cannot be a symbolic link')
    changes = []
    for kind in ('mail', 'weather'):
        original = source / 'assistant' / (kind + '.json')
        staged = candidate / 'assistant' / (kind + '.json')
        if original.is_symlink() or staged.is_symlink():
            raise ValueError('credential configuration cannot be a symbolic link')
        if not original.exists() and not staged.exists():
            continue
        if not original.is_file() or not staged.is_file():
            raise ValueError('credential configuration is missing')
        before = json.loads(original.read_text())
        current = json.loads(staged.read_text())
        expected = remap_credential_config(before, kind, mappings)
        if current != before and current != expected:
            raise ValueError('staged credential configuration changed unexpectedly')
        if current != expected:
            changes.append((staged, json.dumps(expected, ensure_ascii=False, separators=(',', ':')).encode()))
    for path, data in changes:
        fd, temp = tempfile.mkstemp(prefix='.credential-config-', dir=path.parent)
        try:
            with os.fdopen(fd, 'wb') as out:
                out.write(data)
                out.flush()
                os.fsync(out.fileno())
            os.replace(temp, path)
        finally:
            if os.path.exists(temp):
                os.unlink(temp)
    return {'updated_configs': len(changes), 'requires_credential_and_delivery_review': True}



def _portable_follow_builders(source_home, candidate_home, old_home, *, write=False):
    """Rewrite only the known legacy invocation; compare all other bytes."""
    from pathlib import Path
    relative = Path('skills/follow-builders/SKILL.md')
    original, target = Path(source_home)/relative, Path(candidate_home)/relative
    if not original.exists() and not target.exists():
        return
    if original.is_symlink() or target.is_symlink() or not original.is_file() or not target.is_file():
        raise ValueError('skill file needs review')
    before = original.read_bytes()
    old = ('sh '+old_home+'/skills/follow-builders/scripts/run-friday.sh').encode()
    new = b'sh "${REASONIX_HOME}/skills/follow-builders/scripts/run-friday.sh"'
    if before.count(old) > 1:
        raise ValueError('skill invocation needs review')
    expected = before.replace(old, new)
    if write:
        target.write_bytes(expected)
    elif target.read_bytes() != expected:
        raise ValueError('skill invocation differs')

def assemble_legacy_candidate(snapshots, old_paths, destination, instance_root, name, address):
    """Assemble and verify an offline candidate; never install or start it.

    All inputs must be explicitly marked private offline snapshots. The
    operator still has to prove the live source is stopped and coordinate a
    single WeChat owner before activating anything.
    """
    import json
    import os
    import re
    import stat
    from pathlib import Path

    keys = {'home', 'workspace', 'mail', 'weather'}
    if not isinstance(snapshots, dict) or not isinstance(old_paths, dict) or set(snapshots) != keys or set(old_paths) != keys:
        raise ValueError('explicit home, workspace, mail and weather snapshots required')
    if not isinstance(name, str) or not isinstance(address, str) or not name.strip():
        raise ValueError('explicit assistant identity required')
    for path in old_paths.values():
        _absolute_workspace(path)
    _absolute_workspace(instance_root)
    instance = Path(instance_root)
    if instance.parent != Path('/var/lib/asrobot-instances') or not re.fullmatch(r'[0-9a-f]{32}', instance.name):
        raise ValueError('invalid planned instance root')
    destination = Path(destination)
    if not destination.is_absolute() or destination.exists() or destination.is_symlink():
        raise ValueError('new private candidate directory required')
    parent = destination.parent
    info = parent.lstat()
    if parent.is_symlink() or not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError('private candidate parent required')
    parent_real = parent.resolve(strict=True)
    for snapshot in snapshots.values():
        root = Path(snapshot)
        if not root.is_absolute() or root.is_symlink():
            raise ValueError('private offline snapshot required')
        original = root.lstat()
        if (not stat.S_ISDIR(original.st_mode) or original.st_uid != os.getuid() or original.st_mode & 0o077 or
                not (root / '.asrobot-offline-snapshot').is_file() or (root / '.asrobot-offline-snapshot').is_symlink()):
            raise ValueError('private offline snapshot required')
        source_real = root.resolve(strict=True)
        if parent_real == source_real or source_real in parent_real.parents:
            raise ValueError('candidate cannot be placed inside a snapshot')
    destination.mkdir(mode=0o700)
    (destination / 'external').mkdir(mode=0o700)
    (destination / 'state').mkdir(mode=0o700)
    new_workspace = instance_root + '/workspace'
    home = destination / 'home'
    stages = {'home': stage_offline_snapshot(snapshots['home'], home, old_paths['workspace'], new_workspace)}
    for kind in ('workspace', 'mail', 'weather'):
        target = destination / 'workspace' if kind == 'workspace' else destination / 'external' / kind
        stages[kind] = stage_offline_snapshot(snapshots[kind], target, old_paths['workspace'], new_workspace, remap_jobs=False)
    mappings = [(old_paths['home'], instance_root + '/home'), (old_paths['workspace'], new_workspace),
                (old_paths['mail'], instance_root + '/external/mail'), (old_paths['weather'], instance_root + '/external/weather')]
    rewritten = rewrite_staged_credential_configs(snapshots['home'], home, instance_root, mappings)
    _portable_follow_builders(snapshots['home'], home, old_paths['home'], write=True)
    profile = home / 'assistant' / 'profile.json'
    if profile.exists() or profile.is_symlink():
        if profile.is_symlink() or json.loads(profile.read_text()) != {'name': name.strip(), 'address': address.strip()}:
            raise ValueError('existing assistant identity needs review')
    else:
        set_staged_identity(home, name, address)
    workspace_id = destination / 'state' / 'workspace-id'
    fd = os.open(workspace_id, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_NOFOLLOW', 0), 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(old_paths['workspace'])
    verification = verify_legacy_candidate(snapshots, old_paths, destination, instance_root, name, address)
    return {'stages': stages, 'credential_configs': rewritten, 'verification': verification,
            'running': False, 'requires_live_cutover_review': True}


def verify_legacy_candidate(snapshots, old_paths, candidate, instance_root, name, address):
    """Recheck every staged file and permitted transformation before install."""
    import json
    import os
    import re
    import stat
    from pathlib import Path

    keys = {'home', 'workspace', 'mail', 'weather'}
    if not isinstance(snapshots, dict) or not isinstance(old_paths, dict) or set(snapshots) != keys or set(old_paths) != keys:
        raise ValueError('complete snapshots required')
    if not isinstance(name, str) or not isinstance(address, str) or not name.strip():
        raise ValueError('explicit assistant identity required')
    instance = Path(instance_root)
    if instance.parent != Path('/var/lib/asrobot-instances') or not re.fullmatch(r'[0-9a-f]{32}', instance.name):
        raise ValueError('invalid planned instance root')
    for value in old_paths.values():
        _absolute_workspace(value)
    candidate = Path(candidate)
    if not candidate.is_absolute() or candidate.is_symlink():
        raise ValueError('private candidate required')
    info = candidate.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError('private candidate required')
    if {p.name for p in candidate.iterdir()} != {'home','workspace','external','state'}:
        raise ValueError('candidate contents need review')
    for part in ('external', 'state', 'home', 'home/assistant'):
        path = candidate / part
        if path.is_symlink() or not path.is_dir():
            raise ValueError('candidate directory needs review')
    if (Path(snapshots['home']) / 'assistant').is_symlink():
        raise ValueError('snapshot credential directory needs review')
    if {p.name for p in (candidate/'external').iterdir()} != {'mail','weather'} or {p.name for p in (candidate/'state').iterdir()} != {'workspace-id'}:
        raise ValueError('candidate contents need review')
    identity_path = candidate / 'home/assistant/profile.json'
    workspace_id = candidate / 'state/workspace-id'
    if (identity_path.is_symlink() or workspace_id.is_symlink() or
            json.loads(identity_path.read_text()) != {'name':name.strip(), 'address':address.strip()} or
            workspace_id.read_text() != old_paths['workspace']):
        raise ValueError('candidate identity needs review')
    mappings = [(old_paths['home'], instance_root + '/home'), (old_paths['workspace'], instance_root + '/workspace'),
                (old_paths['mail'], instance_root + '/external/mail'), (old_paths['weather'], instance_root + '/external/weather')]
    for kind in ('mail', 'weather'):
        original, staged = Path(snapshots['home']) / 'assistant' / (kind + '.json'), candidate / 'home/assistant' / (kind + '.json')
        if not original.exists() and not staged.exists():
            continue
        if original.is_symlink() or staged.is_symlink() or not original.is_file() or not staged.is_file():
            raise ValueError('credential configuration needs review')
        if json.loads(staged.read_text()) != remap_credential_config(json.loads(original.read_text()), kind, mappings):
            raise ValueError('credential configuration differs')
    home = candidate / 'home'
    verification = verify_staged_snapshot(snapshots['home'], home, old_paths['workspace'], instance_root + '/workspace')
    verification['wechat'] = verify_staged_wechat(snapshots['home'], home)
    verification['mail_proxy_port'] = staged_gmail_proxy_port(home)
    _portable_follow_builders(snapshots['home'], home, old_paths['home'])
    verification['remaining_home'] = verify_private_copy(snapshots['home'], home, _excluded=frozenset({'assistant/state.sqlite3','assistant/mail.json','assistant/weather.json','assistant/profile.json','skills/follow-builders/SKILL.md'}))
    verification['workspace'] = verify_private_copy(snapshots['workspace'], candidate / 'workspace')
    for kind in ('mail', 'weather'):
        verification[kind] = verify_private_copy(snapshots[kind], candidate / 'external' / kind)
    return verification
