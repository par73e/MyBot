#!/usr/bin/env python3
"""Host-only, allowlisted, read-only status collector for the Friday owner."""
import asyncio
import datetime
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import socket
import struct
import subprocess
import urllib.error
import urllib.request

SOCKET = '/run/asrobot-health/friday.sock'
# Configure only through the operator-controlled systemd unit, never requests.
INSTANCE = os.environ.get('FRIDAY_HEALTH_INSTANCE', '')
if not re.fullmatch(r'[0-9a-f]{32}', INSTANCE):
    raise SystemExit('valid owner instance required')
UNIT = 'asrobot-instance-' + INSTANCE + '.service'
OWNER_UID = pwd.getpwnam('asr-' + INSTANCE[:20]).pw_uid
WEBSITE = 'https://asrobot.par73e.cn/asrobot/api/health'
ACTIVITY = Path('/run/asrobot-activity/snapshot.json')

def activity_items():
    try:
        with ACTIVITY.open('rb') as f:
            raw = f.read(65537)
        if len(raw) > 65536:
            raise ValueError('oversized snapshot')
        data = json.loads(raw)
        at = datetime.datetime.fromisoformat(data['collected_at'])
        age = (datetime.datetime.now(datetime.timezone.utc) - at).total_seconds()
        if data['schema_version'] != 1 or not -60 <= age <= 600:
            raise ValueError('stale snapshot')
        result = {}
        for key in ('security', 'access'):
            value = data['items'][key]
            if value.get('status') not in ('ok', 'unknown'):
                raise ValueError('invalid status')
            result[key] = {**value, 'collected_at': at.isoformat()}
        return result
    except (OSError, ValueError, KeyError, TypeError):
        return {key: item(status='unknown') for key in ('security', 'access')}

def item(value=None, status='ok'):
    return {'status': status, 'value': value}

def collect():
    items = {}
    try:
        disk = shutil.disk_usage('/')
        items['disk'] = item({'free_bytes': disk.free, 'total_bytes': disk.total})
    except OSError:
        items['disk'] = item(status='unknown')
    try:
        with open('/proc/meminfo', encoding='ascii') as stream:
            lines = dict(line.split(':', 1) for line in stream if ':' in line)
        def kib(name): return int(lines[name].split()[0]) * 1024
        items['memory'] = item({'available_bytes': kib('MemAvailable'), 'total_bytes': kib('MemTotal')})
    except (OSError, KeyError, ValueError):
        items['memory'] = item(status='unknown')
    try:
        items['load'] = item({'averages': os.getloadavg(), 'cpu_count': os.cpu_count()})
    except OSError:
        items['load'] = item(status='unknown')
    try:
        result = subprocess.run(['/usr/bin/systemctl', 'show', UNIT, '--property=ActiveState', '--value'], capture_output=True, text=True, timeout=2, check=True)
        state = result.stdout.strip()
        items['friday'] = item(state, 'ok' if state == 'active' else 'critical' if state in ('inactive', 'failed') else 'unknown')
    except (OSError, subprocess.SubprocessError):
        items['friday'] = item(status='unknown')
    # Omit unsupported Docker checks rather than adding a permanent warning.
    items.update(activity_items())
    try:
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        with opener.open(WEBSITE, timeout=3) as response:
            items['website'] = item({'http_status': response.status}, 'ok' if response.status == 200 else 'critical')
    except (OSError, urllib.error.URLError, ValueError):
        items['website'] = item(status='unknown')
    values = [x['status'] for x in items.values()]
    overall = 'critical' if 'critical' in values else 'unknown' if 'unknown' in values else 'ok'
    return {'schema_version': 1, 'collected_at': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'status': overall, 'items': items}

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl): return None

slots = asyncio.Semaphore(2)

async def handle(reader, writer):
    try:
        sock = writer.get_extra_info('socket')
        _, uid, _ = struct.unpack('3i', sock.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        if uid != OWNER_UID:
            return
        request = await asyncio.wait_for(reader.readline(), timeout=1)
        if request != b'status\n':
            return
        async with slots:
            result = await asyncio.wait_for(asyncio.to_thread(collect), timeout=7)
        writer.write(json.dumps(result, ensure_ascii=False, allow_nan=False).encode() + b'\n')
        await writer.drain()
    except (OSError, ValueError, asyncio.TimeoutError):
        pass
    finally:
        writer.close()
        await writer.wait_closed()

async def main():
    # systemd socket activation owns the path and its permissions.
    if os.environ.get('LISTEN_PID') != str(os.getpid()) or os.environ.get('LISTEN_FDS') != '1':
        raise SystemExit('systemd socket activation required')
    sock = socket.socket(fileno=3)
    sock.setblocking(False)
    server = await asyncio.start_unix_server(handle, sock=sock, limit=256)
    async with server:
        await server.serve_forever()

if __name__ == '__main__':
    asyncio.run(main())
