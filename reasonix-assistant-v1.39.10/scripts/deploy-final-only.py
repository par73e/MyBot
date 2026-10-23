#!/usr/bin/env python3
"""Install the tested reply filter and enable only bot.weixin.final_only."""
import argparse
import copy
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import sqlite3
import subprocess
import tarfile
import time
import tomllib
import urllib.request

VERSION = 'v1.39.10-assistant.9-final-only'
PREVIOUS = '/opt/mybot/releases/v1.39.10-assistant.8-weather-context'
PREVIOUS_SHA = '200d0ba84f99fff072ba3fd2ccdf6cd1d0063e3824c5b7a69c57216845416fb8'
HOME = Path('/var/lib/mybot/reasonix')
SERVICE = 'mybot-heartbeat.service'


def command(*args):
    return subprocess.run(args, capture_output=True, text=True, check=True, timeout=35).stdout


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def pointer(path, target):
    temporary = path.with_name(path.name + '.final-next')
    temporary.symlink_to(target)
    os.replace(temporary, path)


def atomic(path, body, uid, gid):
    temporary = path.with_name(path.name + '.final-next')
    temporary.write_bytes(body)
    temporary.chmod(0o600)
    os.chown(temporary, uid, gid)
    os.replace(temporary, path)


def jobs_idle():
    database = HOME / 'assistant/state.sqlite3'
    with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
        jobs = [json.loads(r[0]) for r in db.execute('SELECT payload FROM assistant_entities WHERE kind=?', ('job',))]
    if any(j.get('history') and j['history'][-1]['status'] == 'running' for j in jobs):
        raise RuntimeError('background task running; retry when idle')


def chat_idle(cfg):
    control = cfg.get('bot', {}).get('control', {})
    if not control.get('enabled'):
        return
    pid = command('systemctl', 'show', SERVICE, '-p', 'MainPID', '--value').strip()
    env = dict(v.split('=', 1) for v in Path('/proc/' + pid + '/environ').read_text().split('\0') if '=' in v)
    for line in (HOME / '.env').read_text().splitlines():
        key, sep, value = line.partition('=')
        if sep and not key.lstrip().startswith('#'):
            env.setdefault(key.strip().removeprefix('export '), value.strip().strip('\"\''))
    token = env.get(control.get('token_env', ''), '')
    req = urllib.request.Request('http://' + control.get('addr', '127.0.0.1:37913') + '/status', headers={'Authorization': 'Bearer ' + token})
    with urllib.request.urlopen(req, timeout=5) as response:
        status = json.load(response)
    if status['active_sessions']:
        raise RuntimeError('chat turn running; retry when idle')


def main():
    ap = argparse.ArgumentParser()
    for field in ['binary', 'sha256', 'source', 'source-sha256', 'readme']:
        ap.add_argument('--' + field, required=True)
    ap.add_argument('--apply', action='store_true')
    args = ap.parse_args()
    if not args.apply:
        print(json.dumps({'review_only': True, 'version': VERSION, 'config_change': 'bot.weixin.final_only=true'}))
        return
    if os.geteuid() != 0:
        raise RuntimeError('run as root')
    current = Path('/opt/mybot/current')
    release = current.parent / 'releases' / VERSION
    if sha(args.binary) != args.sha256 or sha(args.source) != args.source_sha256:
        raise RuntimeError('artifact checksum mismatch')
    if command(args.binary, '--version').strip() != 'reasonix ' + VERSION:
        raise RuntimeError('wrong binary version')
    if os.readlink(current) != PREVIOUS or sha(current / 'reasonix') != PREVIOUS_SHA or release.exists():
        raise RuntimeError('unexpected release state')
    source_link = Path('/home/ubuntu/Project/MyBot/reasonix-assistant-v1.39.10')
    if not source_link.is_symlink() or os.readlink(source_link) != '/opt/mybot/current/source':
        raise RuntimeError('unexpected source checkout')
    config = HOME / 'config.toml'
    original = config.read_bytes()
    before = tomllib.loads(original.decode())
    original_text = original.decode()
    section = re.search(r'(?m)^\[bot\.weixin\][ \t]*$', original_text)
    if not section or 'final_only' in before['bot']['weixin']:
        raise RuntimeError('unexpected existing reply policy; review before updating')
    updated = (original_text[:section.end()] + '\nfinal_only = true' + original_text[section.end():]).encode()
    expected = copy.deepcopy(before)
    expected['bot']['weixin']['final_only'] = True
    if tomllib.loads(updated.decode()) != expected:
        raise RuntimeError('config change exceeded the one authorized field')
    jobs_idle()
    chat_idle(before)
    protected = [HOME / '.env', HOME / 'assistant/mail.json', HOME / 'assistant/weather.json', HOME / 'weixin/accounts/default.json', HOME / 'prompts/friday.md']
    protected += list((HOME / 'assistant/memory').glob('*.md'))
    protected += list(Path('/var/lib/mybot/weather/qweather').glob('*'))
    mail = json.loads((HOME / 'assistant/mail.json').read_text())
    protected += [Path(a['credential_file']) for a in mail['accounts']]
    fingerprints = {str(p): sha(p) for p in protected if p.is_file()}
    backup = Path('/var/lib/mybot/backups') / ('final-only-' + dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    backup.mkdir(mode=0o700)
    (backup / 'config.toml').write_bytes(original)
    (backup / 'config.toml').chmod(0o600)
    readme = source_link.parent / 'README.md'
    shutil.copyfile(readme, backup / 'README.md')
    release.mkdir(mode=0o755)
    source = release / 'source'
    source.mkdir()
    with tarfile.open(args.source) as tf:
        for member in tf.getmembers():
            if not member.isfile() or Path(member.name).is_absolute() or '..' in Path(member.name).parts:
                raise RuntimeError('invalid source member')
        tf.extractall(source)
    manifest = json.loads((source / 'SOURCE-MANIFEST.json').read_text())
    for filename, digest in manifest['files'].items():
        if sha(source / filename) != digest:
            raise RuntimeError('source checksum mismatch')
    shutil.copyfile(args.binary, release / 'reasonix')
    (release / 'reasonix').chmod(0o755)
    build = {'version': VERSION, 'binary_sha256': args.sha256, 'source_archive_sha256': args.source_sha256, 'source_manifest_sha256': sha(source / 'SOURCE-MANIFEST.json'), 'source_file_count': len(manifest['files'])}
    (release / 'BUILD.json').write_text(json.dumps(build, indent=2))
    owner = pwd.getpwnam('mybot')
    jobs_idle()
    chat_idle(before)
    command('systemctl', 'stop', SERVICE)
    try:
        atomic(config, updated, owner.pw_uid, owner.pw_gid)
        pointer(current, str(release))
        shutil.copyfile(args.readme, readme)
        since = str(int(time.time()))
        command('systemctl', 'start', SERVICE)
        ready = False
        for _ in range(40):
            journal = command('journalctl', '-u', SERVICE, '--since', '@' + since, '--no-pager', '-o', 'cat')
            policy_logged = 'final_only=true' in journal or '"final_only":true' in journal
            if command('systemctl', 'is-active', SERVICE).strip() == 'active' and 'weixin getupdates ready' in journal and policy_logged:
                ready = True
                break
            time.sleep(1)
        if not ready:
            raise RuntimeError('Weixin or final-only policy not ready')
        if {p: sha(p) for p in fingerprints} != fingerprints:
            raise RuntimeError('protected personal settings changed')
        if tomllib.loads(config.read_text()) != expected:
            raise RuntimeError('unexpected config change after restart')
        pid = command('systemctl', 'show', SERVICE, '-p', 'MainPID', '--value').strip()
        if sha('/proc/' + pid + '/exe') != args.sha256:
            raise RuntimeError('running executable differs from local release')
        result = {**build, 'passed': True, 'service_active': True, 'weixin_poll_ready': True, 'final_only': True, 'config_changed_fields': ['bot.weixin.final_only'], 'source_directory': str(source_link), 'local_server_source_identical': True, 'personal_settings_unchanged': True, 'backup': str(backup), 'test_weixin_messages': 0}
        (backup / 'deployment.json').write_text(json.dumps(result, indent=2))
        (backup / 'deployment.json').chmod(0o600)
        print(json.dumps(result, indent=2))
    except Exception:
        command('systemctl', 'stop', SERVICE)
        atomic(config, original, owner.pw_uid, owner.pw_gid)
        pointer(current, PREVIOUS)
        shutil.copyfile(backup / 'README.md', readme)
        command('systemctl', 'start', SERVICE)
        raise


if __name__ == '__main__':
    main()
