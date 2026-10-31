"""Internal preparation of isolated assistant identities.

Reservation alone does not start a process, attach an existing assistant, or
change the user's current scheduler. It is intentionally not exposed by HTTP.
"""
from . import store as s


def reserve_legacy_owner(user_id):
    """Reserve a destination for the verified legacy owner, without attaching it.

    This never changes the live legacy runtime or any existing user records.
    Conflicting WeChat ownership must be reconciled before preparing a copy.
    """
    with s.db() as db:
        db.execute('BEGIN IMMEDIATE')
        user = db.execute('SELECT status FROM users WHERE id=?', (user_id,)).fetchone()
        workspace = db.execute('SELECT legacy_source,native_source FROM workspaces WHERE user_id=?', (user_id,)).fetchone()
        if not user or user['status'] != 'active' or not workspace or workspace['legacy_source'] != 'friday_owner' or workspace['native_source'] != 'friday_full':
            raise ValueError('verified active legacy owner required')
        if db.execute('SELECT 1 FROM bindings WHERE user_id=?', (user_id,)).fetchone():
            raise ValueError('existing platform WeChat binding needs reconciliation')
        previous = db.execute('SELECT id,state FROM assistant_instances WHERE user_id=?', (user_id,)).fetchone()
        if previous:
            if previous['state'] != 'reserved':
                raise ValueError('assistant instance is already active')
            return previous['id']
        identity = s.ident()
        db.execute('INSERT INTO assistant_instances(id,user_id,created,state) VALUES(?,?,?,?)',
                   (identity, user_id, s.now(), 'reserved'))
    return identity


def reserve(user_id):
    """Reserve an opaque instance identity for a new, empty workspace."""
    with s.db() as db:
        db.execute('BEGIN IMMEDIATE')
        user = db.execute('SELECT status FROM users WHERE id=?', (user_id,)).fetchone()
        if not user or user['status'] != 'active':
            raise ValueError('active user required')
        workspace = db.execute('SELECT legacy_source,native_source FROM workspaces WHERE user_id=?', (user_id,)).fetchone()
        if not workspace or workspace['legacy_source'] or workspace['native_source']:
            raise ValueError('existing assistant needs a separate migration')
        if db.execute('SELECT 1 FROM bindings WHERE user_id=?', (user_id,)).fetchone():
            raise ValueError('existing WeChat binding needs a separate migration')
        for table in ('entities', 'messages', 'settings', 'secrets'):
            if db.execute('SELECT 1 FROM ' + table + ' WHERE user_id=?', (user_id,)).fetchone():
                raise ValueError('existing user data needs a separate migration')
        previous = db.execute('SELECT id,state FROM assistant_instances WHERE user_id=?', (user_id,)).fetchone()
        if previous:
            if previous['state'] != 'reserved':
                raise ValueError('assistant instance is already active')
            return previous['id']
        instance_id = s.ident()
        db.execute('INSERT INTO assistant_instances(id,user_id,created,state) VALUES(?,?,?,?)',
                   (instance_id, user_id, s.now(), 'reserved'))
        return instance_id


async def activate_empty(user_id, root):
    """Attach a ready, reserved runtime to a still-empty user.

    This internal operation does not start a runtime. Existing users and their
    data require an explicit migration and cannot be attached through here.
    """
    import re
    import os
    from pathlib import Path
    import httpx

    configured = Path(os.environ.get('ASROBOT_INSTANCE_ROOT', '/run/asrobot-instances'))
    if not Path(root).is_absolute() or Path(root) != configured:
        raise ValueError('instance root differs from platform configuration')
    with s.db() as db:
        row = db.execute("SELECT id FROM assistant_instances WHERE user_id=? AND state='reserved'", (user_id,)).fetchone()
    if not row or not re.fullmatch(r'[0-9a-f]{32}', row['id']):
        raise ValueError('reserved instance required')
    instance_id = row['id']
    folder = Path(root) / instance_id
    try:
        token = (folder / 'native.token').read_text().strip()
        if len(token) < 32:
            raise ValueError('instance credential invalid')
        async with httpx.AsyncClient(transport=httpx.AsyncHTTPTransport(uds=str(folder / 'native.sock')), timeout=5, trust_env=False) as client:
            response = await client.get('http://native/health', headers={'Authorization': 'Bearer ' + token})
            if response.status_code != 200 or response.json() != {'status': 'ok'}:
                raise ValueError('instance is not ready')
            status = await client.get('http://native/v1/status', params={'source': 'friday_full'}, headers={'Authorization': 'Bearer ' + token})
        # Attach only a verified stopped bot with no automatic restart intent.
        # Pairing and explicit start happen after the user owns the instance.
        if status.status_code != 200 or status.json().get('running') is not False or status.json().get('desired_running') is not False:
            raise ValueError('instance is not ready')
    except (OSError, httpx.HTTPError):
        raise ValueError('instance is not ready') from None
    with s.db() as db:
        db.execute('BEGIN IMMEDIATE')
        user = db.execute('SELECT status FROM users WHERE id=?', (user_id,)).fetchone()
        workspace = db.execute('SELECT legacy_source,native_source FROM workspaces WHERE user_id=?', (user_id,)).fetchone()
        if not user or user['status'] != 'active' or not workspace or workspace['legacy_source'] or workspace['native_source']:
            raise ValueError('existing assistant needs a separate migration')
        for table in ('bindings', 'entities', 'messages', 'settings', 'secrets'):
            if db.execute('SELECT 1 FROM ' + table + ' WHERE user_id=?', (user_id,)).fetchone():
                raise ValueError('existing user data needs a separate migration')
        changed = db.execute("UPDATE assistant_instances SET state='active' WHERE user_id=? AND id=? AND state='reserved'", (user_id, instance_id)).rowcount
        if changed != 1:
            raise ValueError('instance reservation changed')
    return instance_id


async def attach_stopped_legacy_owner(user_id, root, expected_account):
    """Atomically attach a verified stopped candidate to its legacy owner.

    Operator-only primitive: the host coordinator MUST first stop and fence the
    legacy runtime, place the account in maintenance, and verify a fresh offline
    snapshot. This function cannot independently inspect host service state.
    """
    import hmac
    import os
    import re
    import unicodedata
    from pathlib import Path
    import httpx

    configured = Path(os.environ.get('ASROBOT_INSTANCE_ROOT', '/run/asrobot-instances'))
    if not Path(root).is_absolute() or Path(root) != configured:
        raise ValueError('instance root differs from platform configuration')
    if (not isinstance(expected_account, str) or not expected_account or len(expected_account) > 240 or
            '/' in expected_account or '\\' in expected_account or expected_account in ('.', '..') or
            any(unicodedata.category(ch) == 'Cc' for ch in expected_account)):
        raise ValueError('invalid WeChat account identity')
    with s.db() as db:
        row = db.execute("SELECT id FROM assistant_instances WHERE user_id=? AND state='reserved'", (user_id,)).fetchone()
        owner = db.execute('SELECT status FROM users WHERE id=?', (user_id,)).fetchone()
        workspace = db.execute('SELECT legacy_source,native_source FROM workspaces WHERE user_id=?', (user_id,)).fetchone()
    if not row or not re.fullmatch(r'[0-9a-f]{32}', row['id']) or not owner or owner['status'] != 'maintenance' or not workspace or workspace['legacy_source'] != 'friday_owner' or workspace['native_source'] != 'friday_full':
        raise ValueError('legacy owner must be reserved and in maintenance')
    identity = row['id']
    folder = configured / identity
    try:
        token = (folder / 'native.token').read_text().strip()
        if len(token) < 32:
            raise ValueError('instance credential invalid')
        async with httpx.AsyncClient(transport=httpx.AsyncHTTPTransport(uds=str(folder / 'native.sock')), timeout=5, trust_env=False) as client:
            headers = {'Authorization':'Bearer ' + token}
            health = await client.get('http://native/health', headers=headers)
            status = await client.get('http://native/v1/status', params={'source':'friday_full'}, headers=headers)
            ownership = await client.get('http://native/v1/ownership', params={'source':'friday_full'}, headers=headers)
        if (health.status_code != 200 or health.json() != {'status':'ok'} or status.status_code != 200 or
                status.json().get('running') is not False or status.json().get('desired_running') is not False or
                ownership.status_code != 200 or ownership.json().get('credential_configured') is not True or
                not isinstance(ownership.json().get('account'), str) or
                not hmac.compare_digest(ownership.json()['account'].encode(), expected_account.encode())):
            raise ValueError('stopped instance ownership could not be verified')
    except (OSError, httpx.HTTPError):
        raise ValueError('stopped instance ownership could not be verified') from None
    with s.db() as db:
        db.execute('BEGIN IMMEDIATE')
        owner = db.execute('SELECT status FROM users WHERE id=?', (user_id,)).fetchone()
        workspace = db.execute('SELECT legacy_source,native_source FROM workspaces WHERE user_id=?', (user_id,)).fetchone()
        if not owner or owner['status'] != 'maintenance' or not workspace or workspace['legacy_source'] != 'friday_owner' or workspace['native_source'] != 'friday_full':
            raise ValueError('legacy ownership changed during verification')
        if db.execute('SELECT 1 FROM bindings WHERE user_id=? OR external_id=?', (user_id, expected_account)).fetchone():
            raise ValueError('WeChat binding needs reconciliation')
        changed = db.execute("UPDATE assistant_instances SET state='active' WHERE user_id=? AND id=? AND state='reserved'", (user_id, identity)).rowcount
        if changed != 1:
            raise ValueError('instance reservation changed')
        db.execute("INSERT INTO bindings(user_id,external_id,status) VALUES(?,?,'bound')", (user_id, expected_account))
        db.execute('UPDATE workspaces SET legacy_source=NULL,native_source=NULL WHERE user_id=?', (user_id,))
        db.execute("UPDATE users SET status='active' WHERE id=?", (user_id,))
    return identity
