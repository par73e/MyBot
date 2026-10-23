"""Acceptance-only transport embedded with go build -overlay, NEVER deploy this binary."""
import json
from pathlib import Path
import sys

r = json.load(sys.stdin)
results = []
for account in r['accounts']:
    data = json.loads(Path(account['credential_file']).read_text())
    result = {'account': account['id'], 'ok': True}
    messages = data.get('messages', [])
    if data.get('error'):
        result.update(ok=False, error='fixture_connection_failed')
    elif r['action'] == 'read':
        found = next(m for m in messages if m['transport_id'] == r['transport_id'])
        result.update(body=found['body'], attachments=[], truncated=False)
    else:
        if r['action'] == 'sync':
            cursor = r.get('cursors', {}).get(account['id'])
            selected = [] if cursor is None else [m for m in messages if m['sequence'] > cursor['sequence']]
            result['cursor'] = {'sequence': max([m['sequence'] for m in messages], default=0)}
        else:
            selected = messages[-r.get('limit', 20):]
        result['messages'] = [{k:v for k,v in m.items() if k not in ('body','sequence')} for m in selected]
    results.append(result)
print(json.dumps({'results': results}))
