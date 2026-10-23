#!/usr/bin/env python3
"""Install assistant.4 with a rollback pointer and server-only state backup."""
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import time


def command(*args):
    return subprocess.run(args, capture_output=True, text=True, check=True).stdout


def pointer(path, target):
    temporary = path.with_name(path.name + '.daily-next')
    temporary.symlink_to(target)
    os.replace(temporary, path)


def main():
    if os.geteuid() != 0:
        raise RuntimeError('Run as root')
    home = Path('/var/lib/mybot/reasonix')
    source = Path('/home/ubuntu/Project/MyBot/reasonix')
    version = 'v1.39.10-assistant.4'
    binary = source / 'bin/reasonix-linux-amd64-daily'
    if version not in command(str(binary), '--version'):
        raise RuntimeError('Unexpected binary version')
    current = Path('/opt/mybot/current')
    previous = os.readlink(current)
    if not previous.endswith('/v1.39.10-assistant.3'):
        raise RuntimeError('Unexpected running version; inspect before deployment')
    release = Path('/opt/mybot/releases') / version
    if release.exists():
        raise RuntimeError('Release already exists')
    protected = [home / 'config.toml', home / '.env', home / 'weixin/accounts/default.json', home / 'prompts/friday.md']
    fingerprints = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in protected}
    memories = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in (home / 'assistant/memory').glob('*.md')}
    stamp = dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    backup = Path('/var/lib/mybot/backups') / ('daily-conversations-' + stamp)
    backup.mkdir(mode=0o700)
    (backup / 'previous-release.txt').write_text(previous)
    (backup / 'previous-release.txt').chmod(0o600)
    database = home / 'assistant/state.sqlite3'
    if database.exists():
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
            jobs = [json.loads(r[0]) for r in db.execute("SELECT payload FROM assistant_entities WHERE kind='job'")]
            if any(j.get('history') and j['history'][-1]['status'] == 'running' for j in jobs):
                raise RuntimeError('A background job is running; defer deployment')
            with sqlite3.connect(backup / 'state.sqlite3') as destination:
                db.backup(destination)
        (backup / 'state.sqlite3').chmod(0o600)
    release.mkdir(mode=0o755)
    shutil.copyfile(binary, release / 'reasonix')
    (release / 'reasonix').chmod(0o755)
    since = str(int(time.time()))
    try:
        pointer(current, str(release))
        command('systemctl', 'restart', 'mybot-heartbeat.service')
        ready = False
        for _ in range(40):
            state = command('systemctl', 'is-active', 'mybot-heartbeat.service').strip()
            journal = command('journalctl', '-u', 'mybot-heartbeat.service', '--since', '@' + since, '--no-pager', '-o', 'cat')
            if state == 'active' and 'weixin getupdates ready' in journal:
                ready = True
                break
            time.sleep(1)
        if not ready:
            raise RuntimeError('Weixin polling did not become ready')
        if fingerprints != {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in protected}:
            raise RuntimeError('Protected configuration or paired state changed')
        if memories != {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in (home / 'assistant/memory').glob('*.md')}:
            raise RuntimeError('Personal memory files changed during deployment')
        result = dict(passed=True, version=version, weixin_poll_ready=True, config_credentials_persona_unchanged=True,
                      personal_memory_files_unchanged=True, backup=str(backup), binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), real_weixin_test_messages=0)
        report = backup / 'deployment.json'
        report.write_text(json.dumps(result, indent=2)); report.chmod(0o600)
        print(json.dumps(result))
    except Exception:
        pointer(current, previous)
        command('systemctl', 'restart', 'mybot-heartbeat.service')
        raise


if __name__ == '__main__':
    main()
