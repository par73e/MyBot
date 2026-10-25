#!/usr/bin/env python3
"""Publish independent AI News; preserve the morning path and all existing clocks."""
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

VERSION = 'v1.39.10-assistant.30-ai-news-count-check'
PREVIOUS = '/opt/mybot/releases/v1.39.10-assistant.29-ai-news-50chars'
PREVIOUS_SHA = '74966d8ef8e754f96b4242da318529ea823c34ce1e534afb25d580c67acbd17a'
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
        print(json.dumps({'review_only': True, 'version': VERSION, 'change': 'standalone seven-event AI News; no morning integration'}))
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
    previous_source = Path(PREVIOUS) / 'source'
    for path in (previous_source / 'internal/assistant').glob('morning*.go'):
        assert sha(path) == sha(source / 'internal/assistant' / path.name), 'morning implementation changed'
    assert sha(previous_source / 'internal/assistant/news_parse.go') == sha(source / 'internal/assistant/news_parse.go')
    def legacy_recent(path):
        return path.read_text().split('func (s NewsService) Recent(',1)[1].split('\nfunc ',1)[0]
    assert legacy_recent(previous_source / 'internal/assistant/news.go') == legacy_recent(source / 'internal/assistant/news.go')
    manifest = json.loads((source / 'SOURCE-MANIFEST.json').read_text())
    assert all(sha(source / name) == digest for name, digest in manifest['files'].items())
    shutil.copyfile(a.binary, release / 'reasonix')
    (release / 'reasonix').chmod(0o755)
    protected = [HOME / '.env', HOME / 'config.toml', HOME / 'assistant/mail.json', HOME / 'assistant/weather.json', HOME / 'weixin/accounts/default.json']
    protected += sorted((HOME / 'assistant/memory').glob('*.md'))
    fingerprints = {str(p): sha(p) for p in protected}
    backup = Path('/var/lib/mybot/backups') / ('standalone-ai-news-' + dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    backup.mkdir(mode=0o700)
    prompt = HOME / 'prompts/friday.md'
    readme = Path('/home/ubuntu/Project/MyBot/README.md')
    shutil.copyfile(prompt, backup / 'friday.md')
    shutil.copyfile(readme, backup / 'README.md')
    account = pwd.getpwnam('mybot')
    def owner(*args):
        return json.loads(run('sudo','-n','-u','mybot','env','HOME=/var/lib/mybot','REASONIX_HOME='+str(HOME),'REASONIX_ASSISTANT=1',str(release / 'reasonix'),'assistant',*args))
    old_config = None
    installed_config = None
    run('systemctl', 'stop', SERVICE)
    try:
        database = HOME / 'assistant/state.sqlite3'
        with sqlite3.connect(database) as src, sqlite3.connect(backup / 'state.sqlite3') as dst:
            src.backup(dst)
        (backup / 'state.sqlite3').chmod(0o600)
        with sqlite3.connect(database) as db:
            old_config = db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0]
        (backup / 'heartbeat-config.json').write_text(old_config)
        current = owner('heartbeat-init')
        installed_config = owner('heartbeat',json.dumps({'action':'set_module_enabled','revision':current['revision'],'module_id':'ai_news_briefing','enabled':True}))['config']
        expected = json.loads(old_config)
        comparison = json.loads(json.dumps(installed_config))
        assert comparison['module_enabled']['ai_news_briefing'] is True
        if 'ai_news_briefing' not in expected['module_enabled']:
            comparison['module_enabled'].pop('ai_news_briefing')
        comparison['revision'] = expected['revision']
        assert comparison == expected, 'existing schedule or morning configuration changed'
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
        result = {'passed': True, 'version': VERSION, 'previous_version': Path(PREVIOUS).name, 'binary_sha256': a.sha256, 'source_file_count': len(manifest['files']), 'service_active': True, 'weixin_ready': True, 'configuration_and_memories_unchanged': True, 'existing_state_preserved': True, 'standalone_news_enabled': True, 'morning_implementation_and_selection_unchanged': True, 'no_recurring_news_trigger_added': True, 'backup': str(backup)}
        (release / 'BUILD.json').write_text(json.dumps(result, indent=2))
        print(json.dumps(result, ensure_ascii=False, indent=2))
    except Exception:
        run('systemctl', 'stop', SERVICE)
        switch(PREVIOUS)
        if old_config is not None:
            with sqlite3.connect(HOME / 'assistant/state.sqlite3') as db:
                actual = json.loads(db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0])
                if installed_config is not None and actual != installed_config:
                    raise RuntimeError('configuration changed after activation; backup retained for manual recovery')
                db.execute("UPDATE assistant_entities SET payload=? WHERE kind='heartbeat_config' AND id='default'",(old_config,))
        shutil.copyfile(backup / 'friday.md', prompt)
        shutil.copyfile(backup / 'README.md', readme)
        os.chown(prompt, account.pw_uid, account.pw_gid)
        run('systemctl', 'start', SERVICE)
        raise


if __name__ == '__main__':
    main()
