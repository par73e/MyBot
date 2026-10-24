#!/usr/bin/env python3
"""Deploy unified module triggers plus the owner's explicit Twitch preference."""
import argparse,copy,datetime as dt,hashlib,json,os,pwd,re,shutil,sqlite3,subprocess,tarfile,time,tomllib
from pathlib import Path
VERSION='v1.39.10-assistant.20-modular-heartbeat'
PREVIOUS='/opt/mybot/releases/v1.39.10-assistant.18-mail-date-override'
PREVIOUS_SHA='962fb75c3571ea90f6f718386c49fef426bcc43c0294c6f3d2996862e71b501f'
HOME=Path('/var/lib/mybot/reasonix');SERVICE='mybot-heartbeat.service'
def run(*a):return subprocess.run(a,capture_output=True,text=True,check=True,timeout=45).stdout
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def owner(binary,*a):return run('sudo','-n','-u','mybot','env','HOME=/var/lib/mybot','REASONIX_HOME='+str(HOME),str(binary),'assistant',*a)
def pointer(p,target):
 t=p.with_name(p.name+'.modular-next');t.symlink_to(target);os.replace(t,p)
def rows(dbpath):
 with sqlite3.connect('file:'+str(dbpath)+'?mode=ro',uri=True) as db:return {(k,i):p for k,i,p in db.execute('select kind,id,payload from assistant_entities')}
def main():
 ap=argparse.ArgumentParser();ap.add_argument('--binary',required=True);ap.add_argument('--sha256',required=True);ap.add_argument('--source',required=True);ap.add_argument('--source-sha256',required=True);ap.add_argument('--readme',required=True);ap.add_argument('--preference',required=True);ap.add_argument('--apply',action='store_true');a=ap.parse_args()
 if not a.apply:print(json.dumps({'version':VERSION,'review_only':True}));return
 if os.geteuid()!=0:raise RuntimeError('run as root')
 current=Path('/opt/mybot/current');binary=Path(a.binary);release=Path('/opt/mybot/releases')/VERSION
 if os.readlink(current)!=PREVIOUS or sha(current/'reasonix')!=PREVIOUS_SHA:raise RuntimeError('production release changed')
 if sha(binary)!=a.sha256 or sha(a.source)!=a.source_sha256:raise RuntimeError('artifact hash mismatch')
 if run(str(binary),'--version').strip()!='reasonix '+VERSION:raise RuntimeError('not the production binary')
 if release.exists():raise RuntimeError('release already exists')
 database=HOME/'assistant/state.sqlite3';before=rows(database)
 if any(json.loads(p).get('history') and json.loads(p)['history'][-1]['status']=='running' for (k,i),p in before.items() if k=='job'):raise RuntimeError('a background task is running; retry when idle')
 if any(k=='heartbeat_config' for k,i in before):raise RuntimeError('unexpected pre-existing unified configuration')
 mail_before=json.loads(owner(current/'reasonix','mail-schedule'));weather_before=json.loads((HOME/'assistant/weather.json').read_text())
 protected=[HOME/'assistant/weather.json',HOME/'assistant/mail.json',HOME/'.env',HOME/'weixin/accounts/default.json',HOME/'prompts/friday.md',Path('/var/lib/mybot/weather/qweather/locations.json'),Path('/var/lib/mybot/weather/qweather/credential.json'),Path('/var/lib/mybot/weather/qweather/friday-ed25519-private.pem')]
 protected+=sorted((HOME/'assistant/memory').glob('*.md'));fingerprints={str(p):sha(p) for p in protected}
 configfile=HOME/'config.toml';original=configfile.read_bytes();cfg=tomllib.loads(original.decode());expected=copy.deepcopy(cfg)
 expected.setdefault('permissions',{}).setdefault('allow',[])
 if 'assistant_heartbeat' not in expected['permissions']['allow']:expected['permissions']['allow'].append('assistant_heartbeat')
 text=original.decode();section=re.search(r'(?ms)^\[permissions\][^\n]*\n(.*?)(?=^\[|\Z)',text)
 if section:
  body=section.group(1);m=re.search(r'(?ms)^allow\s*=\s*\[.*?\]',body)
  assignment='allow = '+json.dumps(expected['permissions']['allow'],ensure_ascii=False)
  body=body[:m.start()]+assignment+body[m.end():] if m else assignment+'\n'+body
  text=text[:section.start(1)]+body+text[section.end(1):]
 else:text+='\n[permissions]\nallow = '+json.dumps(expected['permissions']['allow'])+'\n'
 if tomllib.loads(text)!=expected:raise RuntimeError('config mutation would affect other settings')
 preference=json.loads(Path(a.preference).read_text())
 backup=Path('/var/lib/mybot/backups')/('modular-heartbeat-'+dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'));backup.mkdir(mode=0o700)
 (backup/'config.toml').write_bytes(original);(backup/'config.toml').chmod(0o600)
 readme=Path('/home/ubuntu/Project/MyBot/README.md')
 if readme.exists():shutil.copyfile(readme,backup/'README.md')
 release.mkdir(mode=0o755);source=release/'source';source.mkdir()
 with tarfile.open(a.source) as tf:
  for member in tf.getmembers():
   if not member.isfile() or Path(member.name).is_absolute() or '..' in Path(member.name).parts:raise RuntimeError('unsafe source archive')
  tf.extractall(source)
 manifest=json.loads((source/'SOURCE-MANIFEST.json').read_text())
 for n,h in manifest['files'].items():
  if sha(source/n)!=h:raise RuntimeError('source mismatch')
 shutil.copyfile(binary,release/'reasonix');(release/'reasonix').chmod(0o755)
 account=pwd.getpwnam('mybot');mode=configfile.stat().st_mode&0o777
 run('systemctl','stop',SERVICE)
 try:
  with sqlite3.connect(database) as src,sqlite3.connect(backup/'state.sqlite3') as dst:src.backup(dst)
  (backup/'state.sqlite3').chmod(0o600)
  before=rows(database)
  temporary=configfile.with_name('config.toml.modular-next');temporary.write_text(text);temporary.chmod(mode);os.chown(temporary,account.pw_uid,account.pw_gid);os.replace(temporary,configfile)
  for p in [database,Path(str(database)+'-wal'),Path(str(database)+'-shm')]:
   if p.exists():os.chown(p,account.pw_uid,account.pw_gid)
  migrated=json.loads(owner(release/'reasonix','heartbeat-init'))
  if migrated['weekly_schedule']!=mail_before['weeklySchedule'] or migrated['triggers']['morning']['modules']!=['weather_briefing','mail_check']:raise RuntimeError('routine migration changed defaults')
  if not migrated['module_enabled']['mail_check'] or not migrated['module_enabled']['weather_briefing']:raise RuntimeError('existing modules lost')
  after=rows(database);allowed={('heartbeat_config','default'),('job','mail-check'),('job','weather-morning-check')}
  if any(before.get(k)!=after.get(k) for k in set(before)|set(after) if k not in allowed):raise RuntimeError('migration modified unrelated state')
  for key in [('job','mail-check'),('job','weather-morning-check')]:
   old=json.loads(before[key]);new=json.loads(after[key]);old['enabled']=False
   if old!=new:raise RuntimeError('old job changed beyond retiring its clock')
  memories=json.loads(owner(release/'reasonix','memories'));matches=[m for m in memories if m['subject']==preference['subject'] and not m.get('archived')]
  if matches:
   m=matches[0];request={**preference,'action':'update','id':m['id'],'revision':m['revision']}
  else:request=preference
  saved=json.loads(owner(release/'reasonix','memory',json.dumps(request,ensure_ascii=False)))[0]
  if saved['body']!=preference['body'] or saved['kind']!='long':raise RuntimeError('notification preference not saved')
  pointer(current,str(release));shutil.copyfile(a.readme,readme)
  since=str(int(time.time()));run('systemctl','start',SERVICE);ready=False
  for _ in range(40):
   journal=run('journalctl','-u',SERVICE,'--since','@'+since,'--no-pager','-o','cat')
   if run('systemctl','is-active',SERVICE).strip()=='active' and 'weixin getupdates ready' in journal:ready=True;break
   time.sleep(1)
  if not ready:raise RuntimeError('service/Weixin not ready')
  if fingerprints!={str(p):sha(p) for p in protected}:raise RuntimeError('protected file changed')
  if tomllib.loads(configfile.read_text())!=expected:raise RuntimeError('config changed unexpectedly')
  restored=json.loads(owner(current/'reasonix','heartbeat'))['config']
  if restored!=migrated:raise RuntimeError('restart changed owner configuration')
  pid=run('systemctl','show',SERVICE,'-p','MainPID','--value').strip()
  if sha('/proc/'+pid+'/exe')!=a.sha256:raise RuntimeError('running binary mismatch')
  result={'passed':True,'version':VERSION,'binary_sha256':a.sha256,'source_archive_sha256':a.source_sha256,'source_file_count':len(manifest['files']),'service_active':True,'weixin_ready':True,'single_clock_entry':True,'legacy_clocks_disabled':True,'weekly_defaults_preserved':True,'module_ids':list(migrated['module_enabled']),'memory_subject':saved['subject'],'memory_id':saved['id'],'memory_kind':saved['kind'],'protected_files_unchanged':True,'configuration_change':'only allow assistant_heartbeat','unrelated_database_state_preserved_at_migration':True,'test_weixin_messages':0,'backup':str(backup)}
  (release/'BUILD.json').write_text(json.dumps(result,indent=2));(backup/'deployment.json').write_text(json.dumps(result,indent=2));(backup/'deployment.json').chmod(0o600);print(json.dumps(result,ensure_ascii=False,indent=2))
 except Exception:
  run('systemctl','stop',SERVICE);pointer(current,PREVIOUS);configfile.write_bytes(original);configfile.chmod(mode);os.chown(configfile,account.pw_uid,account.pw_gid)
  if (backup/'state.sqlite3').exists():
   with sqlite3.connect(backup/'state.sqlite3') as src,sqlite3.connect(database) as dst:src.backup(dst)
  if (backup/'README.md').exists():shutil.copyfile(backup/'README.md',readme)
  for p in [database,Path(str(database)+'-wal'),Path(str(database)+'-shm')]:
   if p.exists():os.chown(p,account.pw_uid,account.pw_gid)
  run('systemctl','start',SERVICE);raise
if __name__=='__main__':main()
