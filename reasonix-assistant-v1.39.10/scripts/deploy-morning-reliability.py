#!/usr/bin/env python3
"""Deploy the reviewed morning fix without changing persona or schedule defaults."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import tarfile
import time

VERSION = 'v1.39.10-assistant.35-morning-reliability'
PREVIOUS = '/opt/mybot/releases/v1.39.10-assistant.34-six-features'
PREVIOUS_SHA = '1018f91531ffd4aa8b58ee2246a2a9d82246b70012b230e05f12fc10a35623f0'
STATE = Path('/var/lib/mybot/reasonix')
SERVICE = 'mybot-heartbeat.service'


def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=50)


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def switch(target):
    pointer = Path('/opt/mybot/current.morning-next')
    pointer.symlink_to(target)
    os.replace(pointer, '/opt/mybot/current')


def read_config(database):
    with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
        return db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0]


def main():
    ap = argparse.ArgumentParser()
    for name in ('binary', 'sha256', 'source', 'source-sha256', 'readme'):
        ap.add_argument('--' + name, required=True)
    ap.add_argument('--apply', action='store_true')
    args = ap.parse_args()
    if not args.apply:
        print(json.dumps({'review_only': True, 'version': VERSION}))
        return
    assert os.geteuid() == 0
    assert os.readlink('/opt/mybot/current') == PREVIOUS
    assert sha('/opt/mybot/current/reasonix') == PREVIOUS_SHA
    assert sha(args.binary) == args.sha256 and sha(args.source) == args.source_sha256
    assert run(args.binary, '--version').strip() == 'reasonix ' + VERSION
    release = Path('/opt/mybot/releases') / VERSION
    assert not release.exists()
    release.mkdir(mode=0o755)
    source = release / 'source'
    source.mkdir()
    with tarfile.open(args.source) as archive:
        assert all(m.isfile() and not Path(m.name).is_absolute() and '..' not in Path(m.name).parts for m in archive.getmembers())
        archive.extractall(source)
    manifest = json.loads((source / 'SOURCE-MANIFEST.json').read_text())
    assert all(sha(source / name) == value for name, value in manifest['files'].items())
    report = json.loads((source / 'verification/morning-reliability-build.json').read_text())
    assert report['passed'] and report['binaries']['linux_amd64']['sha256'] == args.sha256
    assert all(sha(source / name) == value for name, value in report['changed_go_sha256'].items())
    old_source = Path(PREVIOUS) / 'source'
    old_go = {str(p.relative_to(old_source)) for p in old_source.rglob('*.go')}
    new_go = {str(p.relative_to(source)) for p in source.rglob('*.go')}
    assert old_go <= new_go, 'unexpected Go source removal'
    changed = {p for p in old_go if sha(old_source / p) != sha(source / p)} | (new_go - old_go)
    assert changed == set(report['changed_go_sha256']) == set(manifest['reviewed_go_changes'])
    shutil.copyfile(args.binary, release / 'reasonix')
    (release / 'reasonix').chmod(0o755)
    protected = [STATE / '.env', STATE / 'config.toml', STATE / 'assistant/mail.json', STATE / 'assistant/weather.json', STATE / 'weixin/accounts/default.json', STATE / 'prompts/friday.md']
    protected += sorted((STATE / 'assistant/memory').glob('*.md'))
    fingerprints = {str(p): sha(p) for p in protected}
    backup = Path('/var/lib/mybot/backups') / ('morning-reliability-' + dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    backup.mkdir(mode=0o700)
    readme = Path('/home/ubuntu/Project/MyBot/README.md')
    shutil.copyfile(readme, backup / 'README.md')
    shutil.copyfile(STATE / 'prompts/friday.md', backup / 'friday.md')
    (backup / 'friday.md').chmod(0o600)
    database = STATE / 'assistant/state.sqlite3'
    # Every mutation after stopping is covered by recovery. Never replace the
    # live database during rollback: newer interactions must remain intact.
    try:
        run('systemctl', 'stop', SERVICE)
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as src, sqlite3.connect(backup / 'state.sqlite3') as dst:
            src.backup(dst)
        (backup / 'state.sqlite3').chmod(0o600)
        config_before = read_config(database)
        database_before = sha(database)
        assert fingerprints == {str(p): sha(p) for p in protected}
        switch(str(release))
        shutil.copyfile(args.readme, readme)
        assert sha(database) == database_before and read_config(database) == config_before
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
        assert sha('/proc/' + pid + '/exe') == args.sha256
        assert read_config(database) == config_before
        assert fingerprints == {str(p): sha(p) for p in protected}
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
            assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
        assert Path('/home/ubuntu/Project/MyBot/reasonix-assistant-v1.39.10').resolve() == source
        result = {'passed': True, 'version': VERSION, 'previous_version': Path(PREVIOUS).name,
                  'binary_sha256': args.sha256, 'source_archive_sha256': args.source_sha256,
                  'source_file_count': len(manifest['files']), 'changed_go_files': sorted(changed),
                  'service_active': True, 'weixin_ready': True, 'running_process_hash_verified': True,
                  'persona_configuration_accounts_and_memory_files_unchanged': True,
                  'heartbeat_configuration_unchanged': True, 'production_state_migrations': False,
                  'database_preserved_before_activation': True, 'database_integrity': 'ok',
                  'workspace_source_follows_current_release': True,
                  'explicit_test_messages_sent': 0, 'backup': str(backup)}
        (release / 'BUILD.json').write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, ensure_ascii=False, indent=2))
    except Exception:
        run('systemctl', 'stop', SERVICE)
        if os.readlink('/opt/mybot/current') != PREVIOUS:
            switch(PREVIOUS)
        shutil.copyfile(backup / 'README.md', readme)
        run('systemctl', 'start', SERVICE)
        raise


if __name__ == '__main__':
    main()
