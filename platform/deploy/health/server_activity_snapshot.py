#!/usr/bin/env python3
"""Root timer exports counts only; the assistant never receives log access."""
import collections
import datetime as dt
import json
import os
from pathlib import Path
import re
import subprocess

OUTPUT = Path('/run/asrobot-activity/snapshot.json')
MAX_BYTES = 32 * 1024 * 1024
ACCESS = re.compile(r'^(\S+) .*?\[([^]]+)\] "[^"]*" (\d{3}) (\d+|-) ')
FAIL = re.compile(r'^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d).*?\[([^]]+)\] (Ban|Unban|Found)\s')

def run(*args):
    return subprocess.run(args, capture_output=True, text=True, check=True, timeout=2).stdout

def tail(path):
    with Path(path).open('rb') as f:
        size = os.fstat(f.fileno()).st_size
        truncated = size > MAX_BYTES
        if truncated:
            f.seek(-MAX_BYTES, 2)
            f.readline()
        return f.read().decode('utf-8', errors='replace').splitlines(), truncated

def log_counts(paths, now, access=False):
    start = now - dt.timedelta(hours=24)
    ips, classes, events = set(), collections.Counter(), collections.Counter()
    count = transferred = invalid = 0
    truncated = False
    for path in paths:
        if not Path(path).exists():
            continue
        lines, cut = tail(path)
        truncated |= cut
        for line in lines:
            match = (ACCESS if access else FAIL).match(line)
            if not match:
                if access:
                    invalid += 1
                continue
            try:
                at = (dt.datetime.strptime(match[2], '%d/%b/%Y:%H:%M:%S %z') if access
                      else dt.datetime.strptime(match[1], '%Y-%m-%d %H:%M:%S').replace(tzinfo=now.tzinfo))
            except ValueError:
                invalid += 1
                continue
            if not start <= at <= now:
                continue
            count += 1
            if access:
                ips.add(match[1])
                classes[match[3][0] + 'xx'] += 1
                transferred += 0 if match[4] == '-' else int(match[4])
            else:
                events[match[3].lower()] += 1
    result = {'window_hours': 24, 'window_start': start.isoformat(), 'window_end': now.isoformat(),
              'truncated': truncated, 'invalid_lines': invalid,
              'coverage': 'current_and_previous_uncompressed_log_only'}
    if access:
        result.update(requests=count, unique_source_ips=len(ips), status_classes=dict(classes), response_bytes=transferred,
                      scope='Nginx shared access.log only; AS Robot dedicated host has access_log off; IPs include bots and are not people.')
    else:
        result['events'] = dict(events)
    return result

def security(now):
    text = run('/usr/bin/fail2ban-client', 'status')
    match = re.search(r'Jail list:\s*(.*)', text)
    if not match:
        raise ValueError('jail list unavailable')
    jails = []
    for name in match[1].split(','):
        name = name.strip()
        if not name:
            continue
        if not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', name) or len(jails) >= 16:
            raise ValueError('invalid jail list')
        status = run('/usr/bin/fail2ban-client', 'status', name)
        row = {'name': name}
        for label, field in [('Currently failed', 'currently_failed'), ('Total failed', 'total_failed'),
                             ('Currently banned', 'currently_banned'), ('Total banned', 'total_banned')]:
            value = re.search(re.escape(label) + r':\s*(\d+)', status)
            if not value:
                raise ValueError('jail counters unavailable')
            row[field] = int(value[1])
        jails.append(row)
    result = {'fail2ban_active': True, 'jails': jails,
              'counter_scope': 'per jail since reset/restart; do not sum as unique attackers',
              'meaning': 'Observed SSH protection counters only, not a security audit or proof of no compromise.'}
    try:
        if not Path('/var/log/fail2ban.log').exists():
            raise OSError('missing log')
        result['recent_log'] = log_counts(['/var/log/fail2ban.log.1', '/var/log/fail2ban.log'], now)
    except OSError:
        result['recent_log'] = {'status': 'unknown'}
    return result

def snapshot(now):
    items = {}
    for key, collect in [('security', lambda: security(now)), ('access', lambda: log_counts(
            ['/var/log/nginx/access.log.1', '/var/log/nginx/access.log'], now, True))]:
        try:
            if key == 'access' and not Path('/var/log/nginx/access.log').exists():
                raise OSError('missing log')
            items[key] = {'status': 'ok', 'value': collect()}
        except (OSError, ValueError, subprocess.SubprocessError):
            items[key] = {'status': 'unknown', 'value': None}
    return {'schema_version': 1, 'collected_at': now.isoformat(), 'items': items}

def main():
    now = dt.datetime.now(dt.timezone(dt.timedelta(hours=8)))
    result = snapshot(now)
    temporary = OUTPUT.with_suffix('.tmp')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
    with os.fdopen(fd, 'w') as f:
        json.dump(result, f, ensure_ascii=False)
    temporary.replace(OUTPUT)

if __name__ == '__main__':
    main()
