#!/usr/bin/env python3
"""Run the native candidate against real QWeather; keep raw location data on host."""
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import pwd
import sys
import subprocess

ROOT=Path('/var/lib/mybot/weather/qweather')
BINARY=Path('/tmp/mybot-weather-candidate')
SHA=sys.argv[1]
if os.geteuid()!=0:raise RuntimeError('run as root')
if hashlib.sha256(BINARY.read_bytes()).hexdigest()!=SHA:raise RuntimeError('candidate checksum mismatch')
user=pwd.getpwnam('mybot')
home=ROOT/'acceptance-home'
for directory in [home,home/'assistant']:
    directory.mkdir(mode=0o700,exist_ok=True);os.chown(directory,user.pw_uid,user.pw_gid)
wake={day:('08:00' if day in ['tuesday','friday'] else '11:00' if day in ['saturday','sunday'] else '10:00') for day in ['monday','tuesday','wednesday','thursday','friday','saturday','sunday']}
config={'enabled':True,'morning_enabled':True,'credential_file':str(ROOT/'credential.json'),'locations_file':str(ROOT/'locations.json'),'wake_times':wake}
p=home/'assistant/weather.json';p.write_text(json.dumps(config));p.chmod(0o600);os.chown(p,user.pw_uid,user.pw_gid)
raws={};report={}
for key,request in [('briefing',{'action':'query','view':'briefing'}),('extra',{'action':'query','components':['air_daily','astronomy']})]:
    proc=subprocess.run(['sudo','-n','-u','mybot','env','HOME=/var/lib/mybot','REASONIX_HOME='+str(home),str(BINARY),'assistant','weather',json.dumps(request)],capture_output=True,text=True,timeout=65)
    if proc.returncode:raise RuntimeError('native weather query failed: '+proc.stderr)
    value=json.loads(proc.stdout);raws[key]=value
    report[key]={name:{'status':part['status'],'error':part.get('error',''),'data_keys':sorted(part.get('data',{}))} for name,part in value['components'].items()}
p=ROOT/'native-verification.json';p.write_text(json.dumps(raws,ensure_ascii=False));p.chmod(0o600);os.chown(p,user.pw_uid,user.pw_gid)
print(json.dumps({'checked_at':dt.datetime.now(dt.timezone.utc).isoformat(),'native_binary_sha256':SHA,'components':report},ensure_ascii=False,indent=2))
