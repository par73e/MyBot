#!/usr/bin/env python3
"""Deploy the explicitly approved GitHub Chinese summary candidate without state migration."""
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

VERSION = 'v1.39.10-assistant.39-github-chinese-summary'
PREVIOUS = '/opt/mybot/releases/v1.39.10-assistant.38-weather-alert-cadence'
PREVIOUS_SHA = 'dad62e25254b60466c917ed581201112ac814a65f13089a0bc04ccae865089f6'
STATE = Path('/var/lib/mybot/reasonix')
SERVICE = 'mybot-heartbeat.service'


def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=50)


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def switch(target):
    pointer = Path('/opt/mybot/current.github-summary-next')
    pointer.symlink_to(target)
    os.replace(pointer, '/opt/mybot/current')


def entities(database):
    with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
        return {(kind, ident): payload for kind, ident, payload in db.execute('SELECT kind,id,payload FROM assistant_entities')}


def protected_paths():
    paths = [STATE / name for name in ['.env', 'config.toml', 'assistant/mail.json', 'assistant/weather.json', 'weixin/accounts/default.json', 'prompts/friday.md', 'credentials/github-pr-monitor.token']]
    paths += sorted((STATE / 'assistant/memory').glob('*.md'))
    weather = json.loads((STATE / 'assistant/weather.json').read_text())
    paths += [Path(weather['credential_file']), Path(weather['locations_file'])]
    credential = json.loads(Path(weather['credential_file']).read_text())
    paths.append(Path(credential['private_key_file']))
    return paths


def prepare_release(args):
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
    report = json.loads((source / 'verification/github-summary-build.json').read_text())
    assert report['passed'] and report['binaries']['linux_amd64']['sha256'] == args.sha256
    assert all(sha(source / name) == value for name, value in report['go_changes_from_38'].items())
    old_source = Path(PREVIOUS) / 'source'
    old_go = {str(p.relative_to(old_source)) for p in old_source.rglob('*.go')}
    new_go = {str(p.relative_to(source)) for p in source.rglob('*.go')}
    assert old_go <= new_go, 'unexpected Go source removal'
    changed = {p for p in old_go if sha(old_source / p) != sha(source / p)} | (new_go - old_go)
    assert changed == set(report['go_changes_from_38']) == set(manifest['reviewed_go_changes'])
    shutil.copyfile(args.binary, release / 'reasonix')
    (release / 'reasonix').chmod(0o755)
    return release, source, manifest, changed


def activate(args, release, source, manifest, changed):
    backup = Path('/var/lib/mybot/backups') / ('github-summary-' + dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    backup.mkdir(mode=0o700)
    readme = Path('/home/ubuntu/Project/MyBot/README.md')
    shutil.copyfile(readme, backup / 'README.md')
    database = STATE / 'assistant/state.sqlite3'
    fingerprints = {str(p): sha(p) for p in protected_paths()}
    stopped = False
    try:
        stopped = True
        run('systemctl', 'stop', SERVICE)
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as src, sqlite3.connect(backup / 'state.sqlite3') as dst:
            src.backup(dst)
        (backup / 'state.sqlite3').chmod(0o600)
        before = entities(database)
        gh = json.loads(before[('github_config', 'default')])
        assert gh['enabled'] and gh['login'] == 'par73e' and gh['poll_seconds'] == 60 and gh['discovery_seconds'] == 300
        switch(str(release))
        shutil.copyfile(args.readme, readme)
        assert entities(database) == before, 'deployment changed personal state'
        assert fingerprints == {str(p): sha(p) for p in protected_paths()}
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
        after = entities(database)
        for key in [('heartbeat_config', 'default'), ('github_config', 'default')]:
            # Native discovery may update its health timestamps after startup.
            old, new = json.loads(before[key]), json.loads(after[key])
            if key[0] == 'github_config':
                for field in ['discovery_at', 'discovery_ok_at', 'discovery_error']:
                    old.pop(field, None)
                    new.pop(field, None)
            assert old == new, 'configuration changed on startup'
        assert fingerprints == {str(p): sha(p) for p in protected_paths()}
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
            assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
        assert Path('/home/ubuntu/Project/MyBot/reasonix-assistant-v1.39.10').resolve() == source
        result = {'passed': True, 'version': VERSION, 'previous_version': Path(PREVIOUS).name,
                  'binary_sha256': args.sha256, 'source_archive_sha256': args.source_sha256,
                  'source_file_count': len(manifest['files']), 'changed_go_files': sorted(changed),
                  'service_active': True, 'weixin_ready': True, 'running_process_hash_verified': True,
                  'persona_configuration_accounts_credentials_and_memory_files_unchanged': True,
                  'routine_and_existing_triggers_unchanged': True, 'github_scope_unchanged': True,
                  'github_poll_seconds': 60, 'github_discovery_seconds': 300,
                  'personal_database_unchanged_at_activation': True, 'production_state_migrations': 'none',
                  'database_integrity': 'ok', 'workspace_source_follows_current_release': True,
                  'explicit_test_messages_sent': 0, 'backup': str(backup)}
        (release / 'BUILD.json').write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, ensure_ascii=False, indent=2))
    except Exception:
        if stopped:
            run('systemctl', 'stop', SERVICE)
            if os.readlink('/opt/mybot/current') != PREVIOUS:
                switch(PREVIOUS)
            shutil.copyfile(backup / 'README.md', readme)
            # Preserve all current DB records; rollback changes only the executable pointer.
            run('systemctl', 'start', SERVICE)
        raise


def main():
    parser = argparse.ArgumentParser()
    for name in ('binary', 'sha256', 'source', 'source-sha256', 'readme'):
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    if not args.apply:
        print(json.dumps({'review_only': True, 'version': VERSION}))
        return
    activate(args, *prepare_release(args))


if __name__ == '__main__':
    main()
