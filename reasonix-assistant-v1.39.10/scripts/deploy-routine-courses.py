#!/usr/bin/env python3
"""Reviewed publication of the global routine gate and owner's private timetable."""
import argparse, datetime as dt, hashlib, json, os, pwd, re, shutil, sqlite3, subprocess, tarfile, time, tomllib
from pathlib import Path

VERSION = 'v1.39.10-assistant.23-routine-courses'
PREVIOUS = '/opt/mybot/releases/v1.39.10-assistant.22-module-closeout'
PREVIOUS_SHA = 'bfd940007b377585995d123109b961d8045562738f5ff4cc87ebb1b7275720d4'
HOME = Path('/var/lib/mybot/reasonix')
SERVICE = 'mybot-heartbeat.service'


def run(*args):
    return subprocess.run(args, capture_output=True, text=True, check=True, timeout=50).stdout


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def owner(binary, *args):
    return json.loads(run('sudo', '-n', '-u', 'mybot', 'env', 'HOME=/var/lib/mybot', 'REASONIX_HOME=' + str(HOME), str(binary), 'assistant', *args))


def rows(path):
    with sqlite3.connect('file:' + str(path) + '?mode=ro', uri=True) as db:
        return {(k, i): p for k, i, p in db.execute('SELECT kind,id,payload FROM assistant_entities')}


def pointer(path, target):
    temp = path.with_name(path.name + '.routine-next')
    temp.symlink_to(target)
    os.replace(temp, path)


def permit_courses(path):
    text = path.read_text()
    before = tomllib.loads(text)
    allow = list(dict.fromkeys(before.get('permissions', {}).get('allow', []) + ['assistant_courses']))
    match = re.search(r'(?ms)^\[permissions\]\s*\n(.*?)(?=^\[|\Z)', text)
    if not match:
        raise RuntimeError('permissions section missing; do not rewrite production config')
    block = match[1]
    updated, count = re.subn(r'(?ms)^allow\s*=\s*\[.*?\]', 'allow = ' + json.dumps(allow), block, count=1)
    if count != 1:
        raise RuntimeError('permissions allow list missing')
    text = text[:match.start(1)] + updated + text[match.end(1):]
    expected = json.loads(json.dumps(before))
    expected['permissions']['allow'] = allow
    if tomllib.loads(text) != expected:
        raise RuntimeError('unexpected config delta')
    path.write_text(text)


def main():
    ap = argparse.ArgumentParser()
    for key in ['binary', 'sha256', 'source', 'source-sha256', 'timetable', 'timetable-sha256', 'readme']:
        ap.add_argument('--' + key, required=True)
    ap.add_argument('--apply', action='store_true')
    a = ap.parse_args()
    if not a.apply:
        print(json.dumps({'review_only': True, 'version': VERSION, 'changes': ['global routine admission', 'private timetable import', 'ten-minute course reminders'], 'service_restart_required': True}))
        return
    if os.geteuid() != 0:
        raise RuntimeError('root required')
    current = Path('/opt/mybot/current')
    release = Path('/opt/mybot/releases') / VERSION
    database = HOME / 'assistant/state.sqlite3'
    if os.readlink(current) != PREVIOUS or sha(current / 'reasonix') != PREVIOUS_SHA:
        raise RuntimeError('production release drifted')
    if sha(a.binary) != a.sha256 or sha(a.source) != a.source_sha256 or sha(a.timetable) != a.timetable_sha256:
        raise RuntimeError('input checksum mismatch')
    if run(a.binary, '--version').strip() != 'reasonix ' + VERSION or release.exists():
        raise RuntimeError('wrong binary or release already exists')
    protected = [HOME / '.env', HOME / 'assistant/mail.json', HOME / 'assistant/weather.json', HOME / 'weixin/accounts/default.json', Path('/var/lib/mybot/weather/qweather/locations.json'), Path('/var/lib/mybot/weather/qweather/credential.json'), Path('/var/lib/mybot/weather/qweather/friday-ed25519-private.pem')]
    protected += sorted((HOME / 'assistant/memory').glob('*.md'))
    fingerprints = {str(p): sha(p) for p in protected}
    before = rows(database)
    if any(k == 'job' and json.loads(v).get('history') and json.loads(v)['history'][-1]['status'] == 'running' for (k, i), v in before.items()):
        raise RuntimeError('a task is running; retry when idle')
    backup = Path('/var/lib/mybot/backups') / ('routine-courses-' + dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    backup.mkdir(mode=0o700)
    config = HOME / 'config.toml'
    prompt = HOME / 'prompts/friday.md'
    readme = Path('/home/ubuntu/Project/MyBot/README.md')
    for path, name in [(config, 'config.toml'), (prompt, 'friday.md'), (readme, 'README.md')]:
        shutil.copyfile(path, backup / name)
        (backup / name).chmod(0o600)
    release.mkdir(mode=0o755)
    source = release / 'source'
    source.mkdir()
    with tarfile.open(a.source) as tf:
        if any(not m.isfile() or Path(m.name).is_absolute() or '..' in Path(m.name).parts for m in tf.getmembers()):
            raise RuntimeError('invalid source archive')
        tf.extractall(source)
    manifest = json.loads((source / 'SOURCE-MANIFEST.json').read_text())
    if any(sha(source / name) != digest for name, digest in manifest['files'].items()):
        raise RuntimeError('source checksum mismatch')
    shutil.copyfile(a.binary, release / 'reasonix')
    (release / 'reasonix').chmod(0o755)
    run('systemctl', 'stop', SERVICE)
    account = pwd.getpwnam('mybot')
    activated = False
    installed = {}
    try:
        with sqlite3.connect(database) as src, sqlite3.connect(backup / 'state.sqlite3') as dst:
            src.backup(dst)
        (backup / 'state.sqlite3').chmod(0o600)
        before = rows(database)
        old = json.loads(before[('heartbeat_config', 'default')])
        permit_courses(config)
        shutil.copyfile(source / 'deploy/tencent/friday.md', prompt)
        os.chown(config, account.pw_uid, account.pw_gid); os.chown(prompt, account.pw_uid, account.pw_gid)
        upgraded = owner(release / 'reasonix', 'heartbeat-init')
        os.chown(a.timetable, account.pw_uid, account.pw_gid); os.chmod(a.timetable, 0o600)
        course = owner(release / 'reasonix', 'courses-import', a.timetable)
        changed = owner(release / 'reasonix', 'heartbeat', json.dumps({'action': 'set_module_enabled', 'revision': upgraded['revision'], 'module_id': 'course_reminder', 'enabled': True}))['config']
        changed = owner(release / 'reasonix', 'heartbeat', json.dumps({'action': 'set_trigger', 'revision': changed['revision'], 'trigger_id': 'course_classes', 'title': '每次课前10分钟提醒', 'mode': 'course', 'modules': ['course_reminder'], 'lead_minutes': 10, 'catch_up_seconds': 60}))['config']
        if old['weekly_schedule'] != changed['weekly_schedule'] or old['date_overrides'] != changed['date_overrides']:
            raise RuntimeError('routine changed unexpectedly')
        if any(changed['module_enabled'][key] != value for key, value in old['module_enabled'].items()):
            raise RuntimeError('existing module state changed')
        if any(changed['triggers'][key] != dict(value, routine_bound=True) for key, value in old['triggers'].items()):
            raise RuntimeError('existing trigger changed unexpectedly')
        installed = rows(database)
        allowed = {('heartbeat_config', 'default'), ('course_config', 'default')}
        if any(before.get(key) != installed.get(key) for key in set(before) | set(installed) if key not in allowed):
            raise RuntimeError('unrelated state changed during upgrade')
        if fingerprints != {str(p): sha(p) for p in protected}:
            raise RuntimeError('credentials or memories changed')
        pointer(current, str(release)); shutil.copyfile(a.readme, readme)
        since = str(int(time.time()))
        run('systemctl', 'start', SERVICE); activated = True
        for _ in range(40):
            journal = run('journalctl', '-u', SERVICE, '--since', '@' + since, '--no-pager', '-o', 'cat')
            if run('systemctl', 'is-active', SERVICE).strip() == 'active' and 'weixin getupdates ready' in journal:
                break
            time.sleep(1)
        else:
            raise RuntimeError('Weixin readiness not confirmed')
        pid = run('systemctl', 'show', SERVICE, '-p', 'MainPID', '--value').strip()
        if sha('/proc/' + pid + '/exe') != a.sha256:
            raise RuntimeError('running binary mismatch')
        result = {'passed': True, 'version': VERSION, 'binary_sha256': a.sha256, 'source_file_count': len(manifest['files']), 'service_active': True, 'weixin_ready': True, 'top_level_routine_gate': True, 'teaching_weeks': course['total_weeks'], 'course_blocks': len(course['courses']), 'lead_minutes': 10, 'routine_unchanged': True, 'existing_modules_preserved': True, 'credentials_and_memories_unchanged': True, 'test_weixin_messages': 0, 'backup': str(backup)}
        (release / 'BUILD.json').write_text(json.dumps(result, indent=2))
        print(json.dumps(result, ensure_ascii=False, indent=2))
    except Exception:
        run('systemctl', 'stop', SERVICE)
        pointer(current, PREVIOUS)
        # Preserve live conversations/mail. Roll back only our exact config rows if unchanged.
        actual = rows(database)
        with sqlite3.connect(database) as db:
            for key in [('heartbeat_config', 'default'), ('course_config', 'default')]:
                if activated and actual.get(key) != installed.get(key):
                    raise RuntimeError('owner changed configuration after activation; backup preserved for manual recovery')
                if key in before:
                    db.execute('INSERT OR REPLACE INTO assistant_entities(kind,id,payload) VALUES(?,?,?)', (*key, before[key]))
                else:
                    db.execute('DELETE FROM assistant_entities WHERE kind=? AND id=?', key)
        for path, name in [(config, 'config.toml'), (prompt, 'friday.md'), (readme, 'README.md')]:
            shutil.copyfile(backup / name, path)
        os.chown(config, account.pw_uid, account.pw_gid); os.chown(prompt, account.pw_uid, account.pw_gid)
        run('systemctl', 'start', SERVICE)
        raise


if __name__ == '__main__':
    main()
