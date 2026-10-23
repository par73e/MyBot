#!/usr/bin/env python3
"""Explicitly authorized server-only assistant.5-mail deployment, with rollback.
Stage a REAL Linux binary first. Never supply the acceptance overlay binary.
Does not run without --apply. Does not send a test Weixin message.
"""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import pwd
import re
import tomllib
import shutil
import sqlite3
import subprocess
import time
from zoneinfo import ZoneInfo

VERSION = 'v1.39.10-assistant.5-mail'
HOME = Path('/var/lib/mybot/reasonix')
SERVICE = 'mybot-heartbeat.service'


def command(*args):
    return subprocess.run(args, capture_output=True, text=True, check=True).stdout


def pointer(path, target):
    tmp = path.with_name(path.name + '.mail-next')
    tmp.symlink_to(target)
    os.replace(tmp, path)


def schedule_permissions(raw):
    original = tomllib.loads(raw)
    rules = original.get('permissions', {}).get('allow', [])
    if 'assistant_mail_schedule' in rules: return raw
    section = re.search(r'(?ms)^\[permissions\]\s*\n(.*?)(?=^\[|\Z)', raw)
    if not section: raise RuntimeError('Missing permissions section; review configuration')
    body = section.group(1)
    match = re.search(r'(?ms)^allow\s*=\s*\[.*?\]', body)
    if not match: raise RuntimeError('Missing explicit permission allow array; review configuration')
    new_rules = rules + ['assistant_mail_schedule']
    new_body = body[:match.start()] + 'allow = ' + json.dumps(new_rules) + body[match.end():]
    updated = raw[:section.start(1)] + new_body + raw[section.end(1):]
    expected = tomllib.loads(raw)
    expected['permissions']['allow'] = new_rules
    if tomllib.loads(updated) != expected: raise RuntimeError('Permission edit changed unrelated fields')
    return updated



def inside_window(now, start, end):
    minute = now.hour * 60 + now.minute
    def clock(value):
        if not re.fullmatch(r"(?:[01][0-9]|2[0-3]):[0-5][0-9]", value):
            raise ValueError('Running hours must use HH:MM')
        h, m = map(int, value.split(':'))
        return h * 60 + m
    lo, hi = clock(start), clock(end)
    if lo == hi: raise ValueError('Running hours must have distinct endpoints')
    return lo <= minute < hi if lo < hi else minute >= lo or minute < hi


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--binary', required=True)
    ap.add_argument('--sha256', required=True)
    ap.add_argument('--outlook-address', required=True)
    ap.add_argument('--window-start', default='08:00')
    ap.add_argument('--window-end', default='00:00')
    ap.add_argument('--apply', action='store_true')
    args = ap.parse_args()
    if not args.apply:
        print('Review only. Requires explicit deployment authorization and --apply.'); return
    inside_window(dt.datetime.now(ZoneInfo('Asia/Shanghai')), args.window_start, args.window_end)
    if os.geteuid() != 0: raise RuntimeError('Run as root')
    if '@' not in args.outlook_address: raise RuntimeError('Invalid Outlook address')
    binary = Path(args.binary)
    if hashlib.sha256(binary.read_bytes()).hexdigest() != args.sha256: raise RuntimeError('Binary digest mismatch')
    if command(str(binary), '--version').strip() != 'reasonix ' + VERSION: raise RuntimeError('Wrong binary version; never deploy fixture builds')
    # Verify runtime dependency before touching the running service.
    command('sudo', '-n', '-u', 'mybot', 'python3', '-I', '-c', 'import imaplib,ssl,email,fcntl,urllib.request;print("OK")')
    current = Path('/opt/mybot/current'); previous = os.readlink(current)
    if not previous.endswith('/v1.39.10-assistant.4'): raise RuntimeError('Unexpected current version; inspect before proceeding')
    release = Path('/opt/mybot/releases') / VERSION
    mail_config = HOME / 'assistant/mail.json'
    if release.exists() or mail_config.exists(): raise RuntimeError('Release/config already exists; inspect instead of overwriting')
    accounts = []
    qq = sorted(Path('/var/lib/mybot/mail/qq').glob('qq-*.json'))
    gmail = sorted(Path('/var/lib/mybot/mail/gmail').glob('gmail-*.json'))
    if len(qq) != 1 or len(gmail) != 2: raise RuntimeError('Expected one QQ and two Gmail credentials')
    for key, provider, path in [('qq', 'qq', qq[0]), ('gmail1', 'gmail', gmail[0]), ('gmail2', 'gmail', gmail[1])]:
        d = json.loads(path.read_text())
        if path.stat().st_mode & 0o077: raise RuntimeError('Credential permissions are too broad')
        a = {'id': key, 'provider': provider, 'address': d['account'], 'credential_file': str(path)}
        if provider == 'qq':
            a.update(folders=['INBOX', 'Junk'], forwarded_accounts=[{'id': 'outlook', 'address': args.outlook_address}])
        else:
            if d.get('scopes') != ['https://www.googleapis.com/auth/gmail.readonly']: raise RuntimeError('Unexpected Gmail scopes')
            a['proxy'] = 'http://127.0.0.1:17890'
        accounts.append(a)
    database = HOME / 'assistant/state.sqlite3'
    with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
        jobs = [json.loads(r[0]) for r in db.execute("SELECT payload FROM assistant_entities WHERE kind='job'")]
        if any(j.get('history') and j['history'][-1]['status'] == 'running' for j in jobs): raise RuntimeError('Background job is active; defer restart')
    config_file = HOME/'config.toml'
    original_config = config_file.read_text()
    updated_config = schedule_permissions(original_config)
    protected = [ HOME/'.env', HOME/'weixin/accounts/default.json', HOME/'prompts/friday.md']
    fingerprints = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in protected}
    stamp = dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    backup = Path('/var/lib/mybot/backups') / ('mail-' + stamp)
    backup.mkdir(mode=0o700)
    (backup/'previous-release.txt').write_text(previous); (backup/'previous-release.txt').chmod(0o600)
    (backup/'config.toml').write_text(original_config); (backup/'config.toml').chmod(0o600)
    user = pwd.getpwnam('mybot')
    release.mkdir(mode=0o755); shutil.copyfile(binary, release/'reasonix'); (release/'reasonix').chmod(0o755)
    # Stop before the consistency snapshot. The rollback restores all mail queue
    # changes along with the release pointer; existing credentials are retained.
    command('systemctl', 'stop', SERVICE)
    try:
        with sqlite3.connect(database) as source, sqlite3.connect(backup/'state.sqlite3') as dest: source.backup(dest)
        (backup/'state.sqlite3').chmod(0o600)
        for p in [database, Path(str(database)+'-wal'), Path(str(database)+'-shm')]:
            if p.exists(): os.chown(p, user.pw_uid, user.pw_gid)
        fd = os.open(mail_config, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as f:
            json.dump({'enabled': True, 'poll_seconds': 60, 'timezone': 'Asia/Shanghai', 'timeWindowStart': args.window_start, 'timeWindowEnd': args.window_end, 'accounts': accounts}, f, indent=2); f.flush(); os.fsync(f.fileno())
        os.chown(mail_config, user.pw_uid, user.pw_gid)
        config_file.write_text(updated_config); config_file.chmod(0o600); os.chown(config_file, user.pw_uid, user.pw_gid)
        pointer(current, str(release))
        since = str(int(time.time())); command('systemctl', 'start', SERVICE)
        ready = False
        for _ in range(45):
            journal = command('journalctl', '-u', SERVICE, '--since', '@'+since, '--no-pager', '-o', 'cat')
            if command('systemctl', 'is-active', SERVICE).strip() == 'active' and 'weixin getupdates ready' in journal:
                with sqlite3.connect('file:'+str(database)+'?mode=ro', uri=True) as db:
                    count = db.execute("SELECT COUNT(*) FROM assistant_entities WHERE kind='mail_cursor'").fetchone()[0]
                    health = [json.loads(r[0]) for r in db.execute("SELECT payload FROM assistant_entities WHERE kind='mail_health'")]
                    jobs = [json.loads(r[0]) for r in db.execute("SELECT payload FROM assistant_entities WHERE kind='job'")]
                monitor = next((j for j in jobs if j['id'] == 'mail-check'), None)
                if not monitor:
                    time.sleep(1); continue
                if not (monitor['kind'] == 'mail_check' and monitor['enabled'] and monitor['interval'] == '60s' and monitor['timezone'] == 'Asia/Shanghai' and monitor.get('timeWindowStart') == args.window_start and monitor.get('timeWindowEnd') == args.window_end):
                    raise RuntimeError('Persisted mail Heartbeat differs from requested running hours')
                quiet = not inside_window(dt.datetime.now(ZoneInfo('Asia/Shanghai')), args.window_start, args.window_end)
                if quiet:
                    if count or health or monitor.get('lastRunAt') or monitor.get('history') or any(j.get('capability') == 'mail' for j in jobs):
                        raise RuntimeError('Mail activity occurred during quiet hours')
                    ready = True; break
                if count == 3 and len(health) == 3 and all(h.get('last_success') and not h.get('error') for h in health):
                    ready = True; break
            time.sleep(1)
        if not ready: raise RuntimeError('Service/mail Heartbeat did not become ready')
        if config_file.read_text() != updated_config: raise RuntimeError('Unexpected config change')
        if fingerprints != {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in protected}: raise RuntimeError('Protected configuration changed')
        report = {'passed': True, 'version': VERSION, 'binary_sha256': args.sha256, 'poll_seconds': 60,
                  'baseline_connections': count, 'baseline_deferred_until_window': quiet, 'mailbox_sources': 4, 'timezone': 'Asia/Shanghai', 'timeWindowStart': args.window_start, 'timeWindowEnd': args.window_end, 'weixin_poll_ready': True, 'backup': str(backup), 'test_weixin_messages': 0}
        (backup/'deployment.json').write_text(json.dumps(report, indent=2)); (backup/'deployment.json').chmod(0o600)
        print(json.dumps(report))
    except Exception:
        command('systemctl', 'stop', SERVICE)
        pointer(current, previous)
        config_file.write_text(original_config); config_file.chmod(0o600); os.chown(config_file, user.pw_uid, user.pw_gid)
        if mail_config.exists(): mail_config.unlink()
        snapshot = backup/'state.sqlite3'
        if snapshot.exists():
            with sqlite3.connect(snapshot) as source, sqlite3.connect(database) as dest: source.backup(dest)
        for p in [database, Path(str(database)+'-wal'), Path(str(database)+'-shm')]:
            if p.exists(): os.chown(p, user.pw_uid, user.pw_gid)
        command('systemctl', 'start', SERVICE)
        raise


if __name__ == '__main__': main()
