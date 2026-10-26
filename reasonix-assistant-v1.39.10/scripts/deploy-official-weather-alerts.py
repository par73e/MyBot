#!/usr/bin/env python3
"""Deploy and activate the approved native official Shanghai weather-alert module; preserve personal state and routine."""
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

VERSION = 'v1.39.10-assistant.37-official-weather-alerts'
PREVIOUS = '/opt/mybot/releases/v1.39.10-assistant.36-github-pr-tracking'
PREVIOUS_SHA = 'd60dd294d00e5ba7b96d45499358f1c98703f53942378d50c6fe4a8ce757c72c'
STATE = Path('/var/lib/mybot/reasonix')
SERVICE = 'mybot-heartbeat.service'


def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=50)


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def switch(target):
    pointer = Path('/opt/mybot/current.alert-next')
    pointer.symlink_to(target)
    os.replace(pointer, '/opt/mybot/current')


def read_config(database):
    with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
        return db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0]


def entities(database):
    with sqlite3.connect('file:'+str(database)+'?mode=ro',uri=True) as db:
        return {(kind,ident):payload for kind,ident,payload in db.execute('SELECT kind,id,payload FROM assistant_entities')}


def verify_heartbeat(old,new):
    assert old['weekly_schedule']==new['weekly_schedule']
    assert old['date_overrides']==new['date_overrides']
    assert old['timezone']==new['timezone']
    for ident,value in old['triggers'].items():
        assert new['triggers'][ident]==value
    for ident,value in old['module_enabled'].items():
        assert new['module_enabled'][ident]==value
    assert set(new['triggers'])==set(old['triggers'])|{'weather_alerts'}
    assert set(new['module_enabled'])==set(old['module_enabled'])|{'weather_alert_check'}
    tr=new['triggers']['weather_alerts']
    assert tr['enabled'] and tr['mode']=='interval' and tr['interval_seconds']==300 and tr['routine_bound']
    assert tr['modules']==['weather_alert_check'] and new['module_enabled']['weather_alert_check']


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
    report = json.loads((source / 'verification/weather-alert-build.json').read_text())
    assert report['passed'] and report['binaries']['linux_amd64']['sha256'] == args.sha256
    assert all(sha(source / name) == value for name, value in report['go_changes_from_deployed_36'].items())
    old_source = Path(PREVIOUS) / 'source'
    old_go = {str(p.relative_to(old_source)) for p in old_source.rglob('*.go')}
    new_go = {str(p.relative_to(source)) for p in source.rglob('*.go')}
    assert old_go <= new_go, 'unexpected Go source removal'
    changed = {p for p in old_go if sha(old_source / p) != sha(source / p)} | (new_go - old_go)
    assert changed == set(report['go_changes_from_deployed_36']) == set(manifest['reviewed_go_changes'])
    shutil.copyfile(args.binary, release / 'reasonix')
    (release / 'reasonix').chmod(0o755)
    protected = [STATE / '.env', STATE / 'config.toml', STATE / 'assistant/mail.json', STATE / 'assistant/weather.json', STATE / 'weixin/accounts/default.json', STATE / 'prompts/friday.md', STATE / 'credentials/github-pr-monitor.token']
    protected += sorted((STATE / 'assistant/memory').glob('*.md'))
    weather_settings = json.loads((STATE / 'assistant/weather.json').read_text())
    protected += [Path(weather_settings['credential_file']), Path(weather_settings['locations_file'])]
    weather_credential = json.loads(Path(weather_settings['credential_file']).read_text())
    protected.append(Path(weather_credential['private_key_file']))
    fingerprints = {str(p): sha(p) for p in protected}
    backup = Path('/var/lib/mybot/backups') / ('official-weather-alerts-' + dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    backup.mkdir(mode=0o700)
    readme = Path('/home/ubuntu/Project/MyBot/README.md')
    shutil.copyfile(readme, backup / 'README.md')
    shutil.copyfile(STATE / 'prompts/friday.md', backup / 'friday.md')
    (backup / 'friday.md').chmod(0o600)
    database = STATE / 'assistant/state.sqlite3'
    # Every mutation after stopping is covered by recovery. Never replace the
    # live database during rollback: newer interactions must remain intact.
    config_before = None
    activated = None
    try:
        run('systemctl', 'stop', SERVICE)
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as src, sqlite3.connect(backup / 'state.sqlite3') as dst:
            src.backup(dst)
        (backup / 'state.sqlite3').chmod(0o600)
        config_before = read_config(database)
        before = entities(database)
        assert fingerprints == {str(p): sha(p) for p in protected}
        switch(str(release))
        shutil.copyfile(args.readme, readme)
        revision = 0
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
            row = db.execute("SELECT payload FROM assistant_entities WHERE kind='alert_config' AND id='default'").fetchone()
            if row:
                revision = json.loads(row[0])['revision']
        request = {'action':'configure','revision':revision,'enabled':True,'poll_seconds':300}
        output = subprocess.check_output(['runuser','-u','mybot','--','env','HOME=/var/lib/mybot','REASONIX_HOME='+str(STATE),'REASONIX_ASSISTANT=1',str(release/'reasonix'),'assistant','weather-alerts',json.dumps(request)],cwd='/var/lib/mybot/workspace',text=True,stderr=subprocess.STDOUT,timeout=45)
        gh = json.loads(output)
        assert gh['enabled'] and gh['region']=='上海市' and gh['poll_seconds']==300
        activated = read_config(database)
        verify_heartbeat(json.loads(config_before),json.loads(activated))
        after = entities(database)
        unexpected = [k for k in set(before)|set(after) if before.get(k)!=after.get(k) and not (k[0].startswith('alert_') or k==('heartbeat_config','default'))]
        assert not unexpected, 'activation changed unrelated personal state'
        assert fingerprints == {str(p):sha(p) for p in protected}
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
        assert read_config(database) == activated
        assert fingerprints == {str(p): sha(p) for p in protected}
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
            assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
        assert Path('/home/ubuntu/Project/MyBot/reasonix-assistant-v1.39.10').resolve() == source
        result = {'passed': True, 'version': VERSION, 'previous_version': Path(PREVIOUS).name,
                  'binary_sha256': args.sha256, 'source_archive_sha256': args.source_sha256,
                  'source_file_count': len(manifest['files']), 'changed_go_files': sorted(changed),
                  'service_active': True, 'weixin_ready': True, 'running_process_hash_verified': True,
                  'persona_configuration_accounts_and_memory_files_unchanged': True, 'paid_weather_credentials_and_locations_unchanged': True,
                  'routine_and_existing_triggers_unchanged': True, 'weather_alert_trigger_enabled': True, 'weather_alert_poll_seconds':300, 'weather_alert_scope':'all official Shanghai municipality, central-city and district warnings', 'production_state_migrations': 'new alert entities and one Heartbeat trigger only',
                  'unrelated_personal_entities_preserved_at_activation': True, 'database_integrity': 'ok',
                  'workspace_source_follows_current_release': True,
                  'explicit_test_messages_sent': 0, 'backup': str(backup)}
        (release / 'BUILD.json').write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, ensure_ascii=False, indent=2))
    except Exception:
        run('systemctl', 'stop', SERVICE)
        if config_before is not None:
            current = read_config(database)
            if activated is not None and current != activated:
                run('systemctl','start',SERVICE)
                raise RuntimeError('Rollback requires merging newer user Heartbeat changes; new release retained')
            with sqlite3.connect(database) as db:
                db.execute("UPDATE assistant_entities SET payload=? WHERE kind='heartbeat_config' AND id='default'", (config_before,))
        if os.readlink('/opt/mybot/current') != PREVIOUS:
            switch(PREVIOUS)
        shutil.copyfile(backup / 'README.md', readme)
        run('systemctl', 'start', SERVICE)
        raise


if __name__ == '__main__':
    main()
