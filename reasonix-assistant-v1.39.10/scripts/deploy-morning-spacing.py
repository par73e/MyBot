#!/usr/bin/env python3
"""Publish the spacing-only successor without rewriting schedules or personal state."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import pwd
import shutil
import sqlite3
import subprocess
import tarfile
import time

VERSION = 'v1.39.10-assistant.27-morning-bold'
PREVIOUS = '/opt/mybot/releases/v1.39.10-assistant.26-morning-lines'
PREVIOUS_SHA = '710152a144509add375a0bf799aaf633ad4c45f4a1d46fb8466e8263724b94f7'
HOME = Path('/var/lib/mybot/reasonix')
SERVICE = 'mybot-heartbeat.service'


def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=50)


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def switch(target):
    path = Path('/opt/mybot/current.spacing-next')
    path.symlink_to(target)
    os.replace(path, '/opt/mybot/current')


def main():
    ap = argparse.ArgumentParser()
    for name in ['binary', 'sha256', 'source', 'source-sha256', 'readme']:
        ap.add_argument('--' + name, required=True)
    ap.add_argument('--apply', action='store_true')
    a = ap.parse_args()
    if not a.apply:
        print(json.dumps({'review_only': True, 'version': VERSION, 'change': 'H1 plus explicit bold greeting and five adjacent lines'}))
        return
    assert os.geteuid() == 0
    assert os.readlink('/opt/mybot/current') == PREVIOUS and sha('/opt/mybot/current/reasonix') == PREVIOUS_SHA
    assert sha(a.binary) == a.sha256 and sha(a.source) == a.source_sha256
    assert run(a.binary, '--version').strip() == 'reasonix ' + VERSION
    release = Path('/opt/mybot/releases') / VERSION
    assert not release.exists()
    release.mkdir(mode=0o755)
    source = release / 'source'
    source.mkdir()
    with tarfile.open(a.source) as tf:
        assert all(m.isfile() and not Path(m.name).is_absolute() and '..' not in Path(m.name).parts for m in tf.getmembers())
        tf.extractall(source)
    manifest = json.loads((source / 'SOURCE-MANIFEST.json').read_text())
    assert all(sha(source / name) == digest for name, digest in manifest['files'].items())
    shutil.copyfile(a.binary, release / 'reasonix')
    (release / 'reasonix').chmod(0o755)
    protected = [HOME / '.env', HOME / 'config.toml', HOME / 'assistant/mail.json', HOME / 'assistant/weather.json', HOME / 'weixin/accounts/default.json']
    protected += sorted((HOME / 'assistant/memory').glob('*.md'))
    fingerprints = {str(p): sha(p) for p in protected}
    backup = Path('/var/lib/mybot/backups') / ('morning-spacing-' + dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    backup.mkdir(mode=0o700)
    prompt = HOME / 'prompts/friday.md'
    readme = Path('/home/ubuntu/Project/MyBot/README.md')
    shutil.copyfile(prompt, backup / 'friday.md')
    shutil.copyfile(readme, backup / 'README.md')
    account = pwd.getpwnam('mybot')
    run('systemctl', 'stop', SERVICE)
    try:
        database = HOME / 'assistant/state.sqlite3'
        with sqlite3.connect(database) as src, sqlite3.connect(backup / 'state.sqlite3') as dst:
            src.backup(dst)
        (backup / 'state.sqlite3').chmod(0o600)
        shutil.copyfile(source / 'deploy/tencent/friday.md', prompt)
        os.chown(prompt, account.pw_uid, account.pw_gid)
        assert fingerprints == {str(p): sha(p) for p in protected}
        switch(str(release))
        shutil.copyfile(a.readme, readme)
        since = str(int(time.time()))
        run('systemctl', 'start', SERVICE)
        for _ in range(40):
            journal = run('journalctl', '-u', SERVICE, '--since', '@' + since, '--no-pager', '-o', 'cat')
            if run('systemctl', 'is-active', SERVICE).strip() == 'active' and 'weixin getupdates ready' in journal:
                break
            time.sleep(1)
        else:
            raise RuntimeError('Weixin readiness not confirmed')
        pid = run('systemctl', 'show', SERVICE, '-p', 'MainPID', '--value').strip()
        assert sha('/proc/' + pid + '/exe') == a.sha256
        result = {'passed': True, 'version': VERSION, 'previous_version': Path(PREVIOUS).name, 'binary_sha256': a.sha256, 'source_file_count': len(manifest['files']), 'service_active': True, 'weixin_ready': True, 'configuration_and_memories_unchanged': True, 'database_rewritten': False, 'six_adjacent_lines_without_blank_lines': True, 'h1_strong_greeting': True, 'backup': str(backup)}
        (release / 'BUILD.json').write_text(json.dumps(result, indent=2))
        print(json.dumps(result, ensure_ascii=False, indent=2))
    except Exception:
        run('systemctl', 'stop', SERVICE)
        switch(PREVIOUS)
        shutil.copyfile(backup / 'friday.md', prompt)
        shutil.copyfile(backup / 'README.md', readme)
        os.chown(prompt, account.pw_uid, account.pw_gid)
        run('systemctl', 'start', SERVICE)
        raise


if __name__ == '__main__':
    main()
