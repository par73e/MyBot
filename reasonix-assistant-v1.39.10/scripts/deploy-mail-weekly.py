#!/usr/bin/env python3
"""Deploy the weekly mail Heartbeat upgrade with an exact binary hash and rollback."""
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
import time

VERSION = 'v1.39.10-assistant.6-mail-weekly'
PREVIOUS = '/opt/mybot/releases/v1.39.10-assistant.5-mail'
PREVIOUS_SHA = '3320a9e03c701827b55175e2cc6e7fb41a78d49112aa87de0945ce0aba83bf69'
HOME = Path('/var/lib/mybot/reasonix')
SERVICE = 'mybot-heartbeat.service'
WEEKLY = {day: {'enabled': True, 'start': ('08:00' if day in ('tuesday', 'friday') else '11:00' if day in ('saturday', 'sunday') else '10:00'), 'end': '24:00'}
          for day in ('monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday')}


def command(*args):
    return subprocess.run(args, capture_output=True, text=True, check=True, timeout=35).stdout


def pointer(path, target):
    temporary = path.with_name(path.name + '.weekly-next')
    temporary.symlink_to(target)
    os.replace(temporary, path)


def owner_call(binary, *args):
    return command('sudo', '-n', '-u', 'mybot', 'env', 'HOME=/var/lib/mybot', 'REASONIX_HOME=' + str(HOME), str(binary), 'assistant', 'mail-schedule', *args)


def restore_database(snapshot, database):
    with sqlite3.connect(snapshot) as src, sqlite3.connect(database) as dst:
        src.backup(dst)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--sha256', required=True)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    if not args.apply:
        print(json.dumps({'review_only': True, 'version': VERSION, 'weeklySchedule': WEEKLY})); return
    if os.geteuid() != 0: raise RuntimeError('Run as root')
    binary = Path(args.binary)
    if hashlib.sha256(binary.read_bytes()).hexdigest() != args.sha256: raise RuntimeError('Binary hash mismatch')
    if command(str(binary), '--version').strip() != 'reasonix ' + VERSION: raise RuntimeError('Wrong binary version')
    current = Path('/opt/mybot/current')
    previous = os.readlink(current)
    if previous != PREVIOUS or hashlib.sha256((current/'reasonix').read_bytes()).hexdigest() != PREVIOUS_SHA:
        raise RuntimeError('Unexpected previous release; inspect first')
    release = Path('/opt/mybot/releases')/VERSION
    if release.exists(): raise RuntimeError('Release already exists')
    database = HOME/'assistant/state.sqlite3'
    with sqlite3.connect('file:'+str(database)+'?mode=ro', uri=True) as db:
        jobs = [json.loads(r[0]) for r in db.execute("SELECT payload FROM assistant_entities WHERE kind='job'")]
    if any(j.get('history') and j['history'][-1]['status']=='running' for j in jobs):
        raise RuntimeError('A background job is running; defer restart')
    protected = [HOME/'config.toml', HOME/'.env', HOME/'assistant/mail.json', HOME/'weixin/accounts/default.json', HOME/'prompts/friday.md']
    fingerprints = {str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in protected}
    backup = Path('/var/lib/mybot/backups')/('mail-weekly-'+dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    backup.mkdir(mode=0o700)
    (backup/'previous-release.txt').write_text(previous); (backup/'previous-release.txt').chmod(0o600)
    release.mkdir(mode=0o755)
    shutil.copyfile(binary, release/'reasonix'); (release/'reasonix').chmod(0o755)
    user = pwd.getpwnam('mybot')
    command('systemctl', 'stop', SERVICE)
    try:
        with sqlite3.connect(database) as src, sqlite3.connect(backup/'state.sqlite3') as dst: src.backup(dst)
        (backup/'state.sqlite3').chmod(0o600)
        for p in [database,Path(str(database)+'-wal'),Path(str(database)+'-shm')]:
            if p.exists(): os.chown(p,user.pw_uid,user.pw_gid)
        state = json.loads(owner_call(release/'reasonix'))
        request = {'action':'update','revision':state['revision'],'timezone':'Asia/Shanghai','weeklySchedule':WEEKLY}
        saved = json.loads(owner_call(release/'reasonix',json.dumps(request)))
        if saved.get('weeklySchedule') != WEEKLY or not saved['enabled'] or saved['interval']!='60s':
            raise RuntimeError('Weekly settings did not persist as requested')
        pointer(current,str(release))
        since = str(int(time.time())); command('systemctl','start',SERVICE)
        ready = False
        for _ in range(40):
            journal=command('journalctl','-u',SERVICE,'--since','@'+since,'--no-pager','-o','cat')
            if command('systemctl','is-active',SERVICE).strip()=='active' and 'weixin getupdates ready' in journal:
                restored=json.loads(owner_call(current/'reasonix'))
                if restored != saved: raise RuntimeError('Restart changed weekly policy')
                ready=True;break
            time.sleep(1)
        if not ready: raise RuntimeError('Service/Weixin did not become ready')
        if fingerprints != {str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in protected}:
            raise RuntimeError('Protected host files changed')
        report={'passed':True,'version':VERSION,'binary_sha256':args.sha256,'schedule':saved,'weixin_poll_ready':True,'host_files_unchanged':True,'backup':str(backup),'test_weixin_messages':0}
        (backup/'deployment.json').write_text(json.dumps(report,indent=2)); (backup/'deployment.json').chmod(0o600)
        print(json.dumps(report))
    except Exception:
        command('systemctl','stop',SERVICE)
        pointer(current,previous)
        if (backup/'state.sqlite3').exists(): restore_database(backup/'state.sqlite3',database)
        for p in [database,Path(str(database)+'-wal'),Path(str(database)+'-shm')]:
            if p.exists(): os.chown(p,user.pw_uid,user.pw_gid)
        command('systemctl','start',SERVICE)
        raise


if __name__ == '__main__': main()
