"""Prepare, but never install, a credential-hardened Friday TOML configuration.

It preserves every unrelated parsed value and rejects layouts it cannot safely
edit. The resulting private file must be reviewed before any deployment.
"""
import argparse
import copy
import json
import os
import re
import tomllib
from pathlib import Path

PROTECTED = [
    '/var/lib/mybot/reasonix/.env',
    '/var/lib/mybot/reasonix/config.toml',
    '/var/lib/mybot/reasonix/credentials',
    '/var/lib/mybot/mail',
    '/var/lib/mybot/weather/qweather/credential.json',
    '/var/lib/mybot/weather/qweather/friday-ed25519-private.pem',
]


def update_table(text, table, updates):
    lines = text.splitlines(keepends=True)
    headers = [(i, (m.group(1) or m.group(2)).strip()) for i, line in enumerate(lines)
               if (m := re.match(r'^\s*(?:\[\[([^\[\]]+)\]\]|\[([^\[\]]+)\])\s*(?:#.*)?$', line.rstrip('\n')))]
    matches = [i for i, name in headers if name == table]
    if len(matches) > 1:
        raise ValueError('duplicate TOML section')
    if matches:
        start = matches[0] + 1
        end = next((i for i, _ in headers if i >= start), len(lines))
    else:
        if text and not text.endswith('\n'):
            lines[-1] += '\n'
        lines.extend(['\n', f'[{table}]\n'])
        start = end = len(lines)
    for key, value in updates.items():
        locations = [i for i in range(start, end) if re.match(r'^\s*' + re.escape(key) + r'\s*=', lines[i])]
        if len(locations) > 1:
            raise ValueError('duplicate TOML key')
        replacement = f'{key} = {json.dumps(value, ensure_ascii=True)}\n'
        if locations:
            i = locations[0]
            old = lines[i].split('=', 1)[1].strip()
            if isinstance(value, list) and not re.fullmatch(r'\[[^\n]*\]\s*(?:#.*)?', old):
                raise ValueError('multiline forbid_read needs manual review')
            if isinstance(value, bool) and not re.fullmatch(r'(true|false)\s*(?:#.*)?', old):
                raise ValueError('unsupported boolean layout')
            lines[i] = replacement
        else:
            lines.insert(end, replacement)
            end += 1
    return ''.join(lines)


def prepare(text):
    before = tomllib.loads(text)
    expected = copy.deepcopy(before)
    sandbox = expected.setdefault('sandbox', {})
    roots = sandbox.get('forbid_read', [])
    if not isinstance(roots, list) or not all(isinstance(p, str) for p in roots):
        raise ValueError('existing forbid_read must be a string array')
    roots = list(dict.fromkeys(roots + PROTECTED))
    sandbox['forbid_read'] = roots
    expected.setdefault('secrets', {}).update(filter_subprocess_env=True, protect_sensitive_files=True)
    result = update_table(text, 'secrets', {'filter_subprocess_env': True, 'protect_sensitive_files': True})
    result = update_table(result, 'sandbox', {'forbid_read': roots})
    if tomllib.loads(result) != expected:
        raise ValueError('unexpected semantic change; review manually')
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', required=True); parser.add_argument('--output', required=True)
    args = parser.parse_args()
    result = prepare(Path(args.input).read_text())
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as out:
        out.write(result); out.flush(); os.fsync(out.fileno())
    print('Prepared private candidate; no running configuration was changed.')


if __name__ == '__main__':
    main()
