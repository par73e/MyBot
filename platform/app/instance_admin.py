"""Operator-only lifecycle for a new, empty platform account.

The privileged host preparation is a separate step. This module never starts
a process, handles credentials, or attaches an existing user's old data.
"""
import argparse
import asyncio
import json
import os
import re
from pathlib import Path

from . import instance_registry, store


def main():
    parser = argparse.ArgumentParser(description='Reserve or attach an empty user assistant.')
    parser.add_argument('action', choices=('list-pending', 'status', 'reserve', 'reserve-legacy', 'activate'))
    parser.add_argument('user_id', nargs='?', help='platform user identifier')
    args = parser.parse_args()
    if args.action == 'list-pending':
        if args.user_id is not None:
            raise SystemExit('list-pending takes no user identifier')
        store.init()
        with store.db() as db:
            rows = db.execute("""SELECT i.id,i.user_id FROM assistant_instances i
                JOIN users u ON u.id=i.user_id JOIN workspaces w ON w.user_id=u.id
                WHERE i.state='reserved' AND u.status='active' AND w.legacy_source IS NULL AND w.native_source IS NULL
                AND NOT EXISTS(SELECT 1 FROM bindings b WHERE b.user_id=u.id)
                AND NOT EXISTS(SELECT 1 FROM entities e WHERE e.user_id=u.id)
                AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.user_id=u.id)
                AND NOT EXISTS(SELECT 1 FROM settings s WHERE s.user_id=u.id)
                AND NOT EXISTS(SELECT 1 FROM secrets s WHERE s.user_id=u.id)
                ORDER BY i.created LIMIT 256""").fetchall()
        print(json.dumps([{'instance_id':row['id'], 'user_id':row['user_id']} for row in rows]))
        return
    if not args.user_id:
        raise SystemExit('platform user identifier required')
    if not re.fullmatch(r'[0-9a-f]{32}', args.user_id):
        raise SystemExit('invalid platform user identifier')
    store.init()
    if args.action == 'status':
        with store.db() as db:
            row = db.execute('SELECT id,state FROM assistant_instances WHERE user_id=?', (args.user_id,)).fetchone()
        result = {'instance_id': row['id'], 'state': row['state']} if row else {'state': 'absent'}
    elif args.action in ('reserve', 'reserve-legacy'):
        identity = (instance_registry.reserve(args.user_id) if args.action == 'reserve'
                    else instance_registry.reserve_legacy_owner(args.user_id))
        result = {'instance_id': identity, 'state': 'reserved'}
    else:
        root = Path(os.environ.get('ASROBOT_INSTANCE_ROOT', '/run/asrobot-instances'))
        identity = asyncio.run(instance_registry.activate_empty(args.user_id, root))
        result = {'instance_id': identity, 'state': 'active'}
    print(json.dumps(result))


if __name__ == '__main__':
    main()
