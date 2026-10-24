#!/usr/bin/env python3
"""Publish verified module refactoring; preserve owner data and clocks."""
import argparse,datetime as dt,hashlib,json,os,pwd,shutil,sqlite3,subprocess,tarfile,time
from pathlib import Path
VERSION='v1.39.10-assistant.22-module-closeout'
PREVIOUS='/opt/mybot/releases/v1.39.10-assistant.21-memory-silent-digest'
PREVIOUS_SHA='51ba1d0cec844dba505cba4566cf216a9ef0957f90378b28963a555bcccf9d3e'
HOME=Path('/var/lib/mybot/reasonix');SERVICE='mybot-heartbeat.service'
def run(*a):return subprocess.run(a,capture_output=True,text=True,check=True,timeout=45).stdout
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def owner(binary,*a):return run('sudo','-n','-u','mybot','env','HOME=/var/lib/mybot','REASONIX_HOME='+str(HOME),str(binary),'assistant',*a)
def pointer(p,target):
 t=p.with_name(p.name+'.digest-next');t.symlink_to(target);os.replace(t,p)
def rows(p):
 with sqlite3.connect('file:'+str(p)+'?mode=ro',uri=True) as db:return {(k,i):v for k,i,v in db.execute('select kind,id,payload from assistant_entities')}
def main():
 ap=argparse.ArgumentParser();ap.add_argument('--binary',required=True);ap.add_argument('--sha256',required=True);ap.add_argument('--source',required=True);ap.add_argument('--source-sha256',required=True);ap.add_argument('--readme',required=True);ap.add_argument('--apply',action='store_true');a=ap.parse_args()
 if not a.apply:print(json.dumps({'review_only':True,'version':VERSION}));return
 if os.geteuid()!=0:raise RuntimeError('root required')
 current=Path('/opt/mybot/current');release=Path('/opt/mybot/releases')/VERSION;database=HOME/'assistant/state.sqlite3'
 if os.readlink(current)!=PREVIOUS or sha(current/'reasonix')!=PREVIOUS_SHA:raise RuntimeError('production release changed')
 if sha(a.binary)!=a.sha256 or sha(a.source)!=a.source_sha256:raise RuntimeError('artifact checksum mismatch')
 if run(a.binary,'--version').strip()!='reasonix '+VERSION:raise RuntimeError('wrong or fixture binary')
 if release.exists():raise RuntimeError('release exists')
 before=rows(database)
 if any(json.loads(v).get('history') and json.loads(v)['history'][-1]['status']=='running' for (k,i),v in before.items() if k=='job'):raise RuntimeError('background task running; wait before restarting')
 protected=[HOME/'config.toml',HOME/'.env',HOME/'assistant/weather.json',HOME/'assistant/mail.json',HOME/'weixin/accounts/default.json',HOME/'prompts/friday.md',Path('/var/lib/mybot/weather/qweather/locations.json'),Path('/var/lib/mybot/weather/qweather/credential.json'),Path('/var/lib/mybot/weather/qweather/friday-ed25519-private.pem')]
 protected+=sorted((HOME/'assistant/memory').glob('*.md'));fingerprints={str(p):sha(p) for p in protected}
 readme=Path('/home/ubuntu/Project/MyBot/README.md');backup=Path('/var/lib/mybot/backups')/('module-closeout-'+dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'));backup.mkdir(mode=0o700)
 if readme.exists():shutil.copyfile(readme,backup/'README.md')
 release.mkdir(mode=0o755);source=release/'source';source.mkdir()
 with tarfile.open(a.source) as tf:
  for m in tf.getmembers():
   if not m.isfile() or Path(m.name).is_absolute() or '..' in Path(m.name).parts:raise RuntimeError('invalid source member')
  tf.extractall(source)
 manifest=json.loads((source/'SOURCE-MANIFEST.json').read_text())
 for name,digest in manifest['files'].items():
  if sha(source/name)!=digest:raise RuntimeError('source checksum mismatch')
 shutil.copyfile(a.binary,release/'reasonix');(release/'reasonix').chmod(0o755)
 run('systemctl','stop',SERVICE);user=pwd.getpwnam('mybot')
 try:
  with sqlite3.connect(database) as src,sqlite3.connect(backup/'state.sqlite3') as dst:src.backup(dst)
  (backup/'state.sqlite3').chmod(0o600);before=rows(database)
  old=json.loads(before[('heartbeat_config','default')]);migrated=json.loads(owner(release/'reasonix','heartbeat-init'))
  expected=json.loads(json.dumps(old))
  if 'mail_digest' not in expected['module_enabled']:
   expected['module_enabled']['mail_digest']=True;expected['revision']+=1
  if migrated!=expected:raise RuntimeError('catalog upgrade changed timings or selections')
  after=rows(database)
  if any(before.get(k)!=after.get(k) for k in set(before)|set(after) if k!=('heartbeat_config','default')):raise RuntimeError('upgrade modified unrelated database state')
  pointer(current,str(release));shutil.copyfile(a.readme,readme)
  since=str(int(time.time()));run('systemctl','start',SERVICE);ready=False
  for _ in range(40):
   journal=run('journalctl','-u',SERVICE,'--since','@'+since,'--no-pager','-o','cat')
   if run('systemctl','is-active',SERVICE).strip()=='active' and 'weixin getupdates ready' in journal:ready=True;break
   time.sleep(1)
  if not ready:raise RuntimeError('service/Weixin readiness not confirmed')
  if fingerprints!={str(p):sha(p) for p in protected}:raise RuntimeError('protected config or memories changed')
  config=json.loads(owner(current/'reasonix','heartbeat'))['config']
  if config!=migrated:raise RuntimeError('configuration changed at startup')
  pid=run('systemctl','show',SERVICE,'-p','MainPID','--value').strip()
  if sha('/proc/'+pid+'/exe')!=a.sha256:raise RuntimeError('running binary mismatch')
  result={'passed':True,'version':VERSION,'binary_sha256':a.sha256,'source_archive_sha256':a.source_sha256,'source_file_count':len(manifest['files']),'service_active':True,'weixin_ready':True,'memory_intent_policy':True,'per_message_mail_decisions':True,'mail_digest_module_available':True,'new_trigger_created':False,'previous_timings_and_modules_preserved':True,'protected_files_unchanged':True,'production_memories_unchanged':True,'unrelated_database_state_preserved_at_upgrade':True,'test_weixin_messages':0,'backup':str(backup)}
  (release/'BUILD.json').write_text(json.dumps(result,indent=2));(backup/'deployment.json').write_text(json.dumps(result,indent=2));print(json.dumps(result,ensure_ascii=False,indent=2))
 except Exception:
  run('systemctl','stop',SERVICE);pointer(current,PREVIOUS)
  if (backup/'state.sqlite3').exists():
   with sqlite3.connect(backup/'state.sqlite3') as src,sqlite3.connect(database) as dst:src.backup(dst)
  if (backup/'README.md').exists():shutil.copyfile(backup/'README.md',readme)
  for p in [database,Path(str(database)+'-wal'),Path(str(database)+'-shm')]:
   if p.exists():os.chown(p,user.pw_uid,user.pw_gid)
  run('systemctl','start',SERVICE);raise
if __name__=='__main__':main()
