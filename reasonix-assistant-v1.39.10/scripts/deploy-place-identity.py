#!/usr/bin/env python3
"""Deploy bound place identities and add only owner-approved region fields."""
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

VERSION='v1.39.10-assistant.17-place-identity'
PREVIOUS='/opt/mybot/releases/v1.39.10-assistant.16-mail-boundaries'
PREVIOUS_SHA='5504e4afbafb365e1bd4dca611c1ac97d2156ca7ee77931973db12a4d39b610a'
HOME=Path('/var/lib/mybot/reasonix');SERVICE='mybot-heartbeat.service'
PRIVATE=Path('/var/lib/mybot/weather/qweather')
WAKE={day:('08:00' if day in ['tuesday','friday'] else '11:00' if day in ['saturday','sunday'] else '10:00') for day in ['monday','tuesday','wednesday','thursday','friday','saturday','sunday']}


def command(*args):return subprocess.run(args,capture_output=True,text=True,check=True,timeout=35).stdout

def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def pointer(path,target):
    temporary=path.with_name(path.name+'.place-identity-next');temporary.symlink_to(target);os.replace(temporary,path)

def owner(binary,*args):return command('sudo','-n','-u','mybot','env','HOME=/var/lib/mybot','REASONIX_HOME='+str(HOME),str(binary),'assistant',*args)

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--binary',required=True);ap.add_argument('--sha256',required=True);ap.add_argument('--source',required=True);ap.add_argument('--source-sha256',required=True);ap.add_argument('--readme',required=True);ap.add_argument('--apply',action='store_true');args=ap.parse_args()
    if not args.apply:print(json.dumps({'review_only':True,'version':VERSION,'wake_times':WAKE}));return
    if os.geteuid()!=0:raise RuntimeError('run as root')
    binary=Path(args.binary);archive=Path(args.source);current=Path('/opt/mybot/current');release=Path('/opt/mybot/releases')/VERSION
    if sha(binary)!=args.sha256 or sha(archive)!=args.source_sha256:raise RuntimeError('artifact checksum mismatch')
    if command(str(binary),'--version').strip()!='reasonix '+VERSION:raise RuntimeError('wrong binary version')
    if os.readlink(current)!=PREVIOUS or sha(current/'reasonix')!=PREVIOUS_SHA:raise RuntimeError('previous release changed')
    if release.exists():raise RuntimeError('release already exists')
    import tomllib
    if not tomllib.loads((HOME/'config.toml').read_text())['bot']['weixin'].get('final_only'):raise RuntimeError('existing final-only output is not enabled')
    weatherfile=HOME/'assistant/weather.json';source_link=Path('/home/ubuntu/Project/MyBot/reasonix-assistant-v1.39.10');readme=source_link.parent/'README.md'
    if not weatherfile.is_file() or not source_link.is_symlink() or os.readlink(source_link)!='/opt/mybot/current/source':raise RuntimeError('unexpected existing weather configuration/source link')
    database=HOME/'assistant/state.sqlite3'
    with sqlite3.connect('file:'+str(database)+'?mode=ro',uri=True) as db:
        jobs=[json.loads(r[0]) for r in db.execute("SELECT payload FROM assistant_entities WHERE kind='job'")]
    if any(j.get('history') and j['history'][-1]['status']=='running' for j in jobs):raise RuntimeError('background task running; defer restart')
    protected=[weatherfile,HOME/'config.toml',HOME/'.env',HOME/'assistant/mail.json',HOME/'weixin/accounts/default.json',HOME/'prompts/friday.md',PRIVATE/'credential.json',PRIVATE/'friday-ed25519-private.pem']
    protected+=sorted((HOME/'assistant/memory').glob('*.md'))
    locations=Path(json.loads(weatherfile.read_text())['locations_file'])
    if locations!=PRIVATE/'locations.json':raise RuntimeError('unexpected locations path')
    locations_before=locations.read_bytes();location_stat=locations.stat()
    expected=json.loads(locations_before)
    for key,region in {'home':'黄浦','school':'临港'}.items():
        if key not in expected['places']:raise RuntimeError('missing configured place')
        expected['places'][key]['region']=region
    def write_locations(raw):
        temporary=locations.with_name(locations.name+'.place-identity-next')
        with temporary.open('xb') as f:f.write(raw)
        os.chmod(temporary,location_stat.st_mode&0o777);os.chown(temporary,location_stat.st_uid,location_stat.st_gid);os.replace(temporary,locations)
    fingerprints={str(p):sha(p) for p in protected};mail_before=json.loads(owner(current/'reasonix','mail-schedule'))
    backup=Path('/var/lib/mybot/backups')/('place-identity-'+dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'));backup.mkdir(mode=0o700)
    shutil.copy2(locations,backup/'locations.json');(backup/'locations.json').chmod(0o600)
    if readme.exists():shutil.copyfile(readme,backup/'README.md')
    release.mkdir(mode=0o755);source=release/'source';source.mkdir()
    with tarfile.open(archive) as tf:
        for member in tf.getmembers():
            if not member.isfile() or Path(member.name).is_absolute() or '..' in Path(member.name).parts:raise RuntimeError('invalid source member')
        tf.extractall(source)
    manifest=json.loads((source/'SOURCE-MANIFEST.json').read_text())
    for filename,digest in manifest['files'].items():
        if sha(source/filename)!=digest:raise RuntimeError('extracted source checksum mismatch')
    shutil.copyfile(binary,release/'reasonix');(release/'reasonix').chmod(0o755)
    build={'version':VERSION,'binary_sha256':args.sha256,'source_archive_sha256':args.source_sha256,'source_manifest_sha256':sha(source/'SOURCE-MANIFEST.json'),'source_file_count':len(manifest['files']),'project_readme_sha256':sha(args.readme)}
    (release/'BUILD.json').write_text(json.dumps(build,indent=2));user=pwd.getpwnam('mybot')
    regions_updated=False
    command('systemctl','stop',SERVICE)
    try:
        with sqlite3.connect(database) as src,sqlite3.connect(backup/'state.sqlite3') as dst:src.backup(dst)
        (backup/'state.sqlite3').chmod(0o600)
        with sqlite3.connect('file:'+str(database)+'?mode=ro',uri=True) as db:
            state_before=db.execute('SELECT kind,id,payload FROM assistant_entities ORDER BY kind,id').fetchall()
            notifications_before=db.execute('SELECT owner,id,payload FROM assistant_notifications ORDER BY owner,id').fetchall()
        if locations.read_bytes()!=locations_before:raise RuntimeError('locations changed while staging')
        write_locations((json.dumps(expected,ensure_ascii=False,indent=2)+'\n').encode())
        regions_updated=True
        pointer(current,str(release));shutil.copyfile(args.readme,readme)
        for p in [database,Path(str(database)+'-wal'),Path(str(database)+'-shm')]:
            if p.exists():os.chown(p,user.pw_uid,user.pw_gid)
        with sqlite3.connect('file:'+str(database)+'?mode=ro',uri=True) as db:
            assert db.execute('SELECT kind,id,payload FROM assistant_entities ORDER BY kind,id').fetchall()==state_before
            assert db.execute('SELECT owner,id,payload FROM assistant_notifications ORDER BY owner,id').fetchall()==notifications_before
        since=str(int(time.time()));command('systemctl','start',SERVICE);ready=False
        for _ in range(40):
            journal=command('journalctl','-u',SERVICE,'--since','@'+since,'--no-pager','-o','cat')
            if command('systemctl','is-active',SERVICE).strip()=='active' and 'weixin getupdates ready' in journal:
                ready=True;break
            time.sleep(1)
        if not ready:raise RuntimeError('service/Weixin not ready')
        if fingerprints!={str(p):sha(p) for p in protected}:raise RuntimeError('protected files changed')
        if json.loads(locations.read_text())!=expected:raise RuntimeError('unexpected locations mutation')
        if json.loads(owner(current/'reasonix','mail-schedule'))!=mail_before:raise RuntimeError('mail policy changed')
        status=json.loads(owner(current/'reasonix','weather'))
        if status['wake_times']!=WAKE or not status['morning_enabled']:raise RuntimeError('weather schedule not active')
        day=dt.datetime.now(dt.timezone(dt.timedelta(hours=8))).strftime('%A').lower()
        key=expected['default_by_weekday'][day]
        if status['candidate']['place_id']!=key or status['candidate']['region']!=expected['places'][key]['region']:raise RuntimeError('candidate not bound to selected place region')
        for key,region in {'home':'黄浦','school':'临港'}.items():
            ref=status['places'][key]
            if ref!={k:v for k,v in dict(expected['places'][key],place_id=key).items() if k in ['place_id','name','region']}:raise RuntimeError('status detached location fields')
            if ref['region']!=region:raise RuntimeError('incorrect region')
        with sqlite3.connect('file:'+str(database)+'?mode=ro',uri=True) as db:
            jobs=[json.loads(r[0]) for r in db.execute("SELECT payload FROM assistant_entities WHERE kind='job'")]
        monitor=next(j for j in jobs if j['id']=='weather-morning-check')
        if monitor['kind']!='weather_check' or monitor['interval']!='60s' or not monitor['enabled']:raise RuntimeError('weather native scheduler missing')
        pid=command('systemctl','show',SERVICE,'-p','MainPID','--value').strip()
        if sha('/proc/'+pid+'/exe')!=args.sha256:raise RuntimeError('running executable checksum mismatch')
        result={**build,'passed':True,'service_active':True,'weixin_poll_ready':True,'source_directory':str(source_link),'local_server_source_identical':True,'protected_settings_and_personal_memories_unchanged':True,'weather_wake_times':WAKE,'timezone':'Asia/Shanghai','backup':str(backup),'test_weixin_messages':0,'reply_policy_final_only':True,'state_unchanged_before_restart':True,'regions_added':{'home':'黄浦','school':'临港'},'coordinates_and_other_location_fields_unchanged':True,'structured_status_verified':True}
        (backup/'deployment.json').write_text(json.dumps(result,indent=2));(backup/'deployment.json').chmod(0o600);print(json.dumps(result,ensure_ascii=False,indent=2))
    except Exception:
        command('systemctl','stop',SERVICE);pointer(current,PREVIOUS)
        if regions_updated:write_locations(locations_before)
        if (backup/'README.md').exists():shutil.copyfile(backup/'README.md',readme)
        for p in [database,Path(str(database)+'-wal'),Path(str(database)+'-shm')]:
            if p.exists():os.chown(p,user.pw_uid,user.pw_gid)
        command('systemctl','start',SERVICE);raise

if __name__=='__main__':main()
