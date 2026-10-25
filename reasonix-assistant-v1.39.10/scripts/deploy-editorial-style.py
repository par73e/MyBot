#!/usr/bin/env python3
"""Install reviewed Friday expression policy without changing personal state."""
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

VERSION = 'v1.39.10-assistant.33-editorial-style'
PREVIOUS = '/opt/mybot/releases/v1.39.10-assistant.32-url-chunks'
PREVIOUS_SHA = '947789c3c6be9f3d8d89dd53a4ea42d6af0c3e3ff244e174e61d4a1f3f934438'
HOME = Path('/var/lib/mybot/reasonix')
SERVICE = 'mybot-heartbeat.service'
PROMPT_SHA = '62e47e89244fbb6fb005b633476efdfd15ac5bfda5582b796b0197269b1c003a'
CHANGED_GO = {'internal/assistant/hot_news_prompt.go', 'internal/assistant/morning_prompt.go'}


def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=50)


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def switch(target):
    pointer = Path('/opt/mybot/current.editorial-next')
    pointer.symlink_to(target)
    os.replace(pointer, '/opt/mybot/current')


def main():
    ap = argparse.ArgumentParser()
    for name in ('binary', 'sha256', 'source', 'source-sha256', 'readme'):
        ap.add_argument('--' + name, required=True)
    ap.add_argument('--apply', action='store_true')
    a = ap.parse_args()
    if not a.apply:
        print(json.dumps({'review_only': True, 'version': VERSION}))
        return
    assert os.geteuid() == 0
    assert os.readlink('/opt/mybot/current') == PREVIOUS
    assert sha('/opt/mybot/current/reasonix') == PREVIOUS_SHA
    prompt = HOME / 'prompts/friday.md'
    assert sha(prompt) == PROMPT_SHA, 'production persona changed after review'
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
    old_source = Path(PREVIOUS) / 'source'
    old_go = {str(p.relative_to(old_source)) for p in old_source.rglob('*.go')}
    new_go = {str(p.relative_to(source)) for p in source.rglob('*.go')}
    assert old_go == new_go, 'unexpected Go file additions or removals'
    changed = {p for p in old_go if sha(old_source / p) != sha(source / p)}
    assert changed == CHANGED_GO, 'unexpected compiled source delta: ' + repr(changed)
    shutil.copyfile(a.binary, release / 'reasonix')
    (release / 'reasonix').chmod(0o755)
    protected = [HOME / '.env', HOME / 'config.toml', HOME / 'assistant/mail.json', HOME / 'assistant/weather.json', HOME / 'weixin/accounts/default.json']
    protected += sorted((HOME / 'assistant/memory').glob('*.md'))
    fingerprints = {str(p): sha(p) for p in protected}
    backup = Path('/var/lib/mybot/backups') / ('editorial-style-' + dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    backup.mkdir(mode=0o700)
    readme = Path('/home/ubuntu/Project/MyBot/README.md')
    shutil.copyfile(prompt, backup / 'friday.md')
    shutil.copyfile(readme, backup / 'README.md')
    account = pwd.getpwnam('mybot')
    (backup / 'friday.md').chmod(0o600)
    run('systemctl', 'stop', SERVICE)
    try:
        database = HOME / 'assistant/state.sqlite3'
        with sqlite3.connect(database) as src, sqlite3.connect(backup / 'state.sqlite3') as dst:
            src.backup(dst)
        (backup / 'state.sqlite3').chmod(0o600)
        with sqlite3.connect(database) as db:
            config_before = db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0]
        assert fingerprints == {str(p): sha(p) for p in protected}
        temporary = prompt.with_name('friday.md.editorial-next')
        shutil.copyfile(source / 'deploy/tencent/friday.md', temporary)
        temporary.chmod(0o600)
        os.chown(temporary, account.pw_uid, account.pw_gid)
        os.replace(temporary, prompt)
        assert sha(prompt) == manifest['files']['deploy/tencent/friday.md']
        with sqlite3.connect(database) as db:
            assert db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0] == config_before
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
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
            assert db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0] == config_before
        result = {'passed': True, 'version': VERSION, 'previous_version': Path(PREVIOUS).name, 'binary_sha256': a.sha256,
                  'prompt_sha256': sha(prompt), 'source_file_count': len(manifest['files']),
                  'changed_go_files': sorted(changed), 'service_active': True, 'weixin_ready': True,
                  'protected_configuration_and_memories_verified_before_activation': True,
                  'heartbeat_configuration_unchanged': True, 'production_state_migrations': False,
                  'explicit_test_messages_sent': 0, 'backup': str(backup)}
        (release / 'BUILD.json').write_text(json.dumps(result, indent=2))
        print(json.dumps(result, ensure_ascii=False, indent=2))
    except Exception:
        run('systemctl', 'stop', SERVICE)
        switch(PREVIOUS)
        shutil.copyfile(backup / 'friday.md', prompt)
        prompt.chmod(0o600)
        os.chown(prompt, account.pw_uid, account.pw_gid)
        shutil.copyfile(backup / 'README.md', readme)
        run('systemctl', 'start', SERVICE)
        raise


if __name__ == '__main__':
    main()
