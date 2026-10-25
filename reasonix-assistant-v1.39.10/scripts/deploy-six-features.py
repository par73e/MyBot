#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Reviewed six-feature activation with state-preserving failure rollback."""
import argparse, datetime as dt, hashlib, json, os, pwd, shutil, sqlite3, subprocess, tarfile, time
from pathlib import Path
VERSION='v1.39.10-assistant.34-six-features'
PREVIOUS='/opt/mybot/releases/v1.39.10-assistant.33-editorial-style'
PREVIOUS_SHA='41c7441b6d980ad82fe7ad3bc016f4ea3f129b8798d234694d74a7f2f7d06a5f'
PROMPT_SHA='4f611de751996e590d8d29a86bdf7f5aa2b52818aa89f991f436d81201ff798e'
HOME=Path('/var/lib/mybot/reasonix');SERVICE='mybot-heartbeat.service'
def run(*args,cwd=None):return subprocess.check_output(args,cwd=cwd,text=True,stderr=subprocess.STDOUT,timeout=55)
def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def switch(target):
    p=Path('/opt/mybot/current.six-next');p.symlink_to(target);os.replace(p,'/opt/mybot/current')
def entries(db):return {(k,i):v for k,i,v in db.execute('SELECT kind,id,payload FROM assistant_entities')}
def verify_config(old,new):
    assert old['date_overrides']==new['date_overrides']
    for day,w in new['weekly_schedule'].items():
        assert w['enabled']==old['weekly_schedule'][day]['enabled'] and w['end']=='23:30'
        assert w['start']==('09:50' if day in ['monday','wednesday','thursday'] else '08:00' if day in ['tuesday','friday'] else '11:00')
    for ident,tr in old['triggers'].items():assert new['triggers'][ident]==tr
    for ident,enabled in old['module_enabled'].items():assert new['module_enabled'][ident]==enabled
    assert new['triggers']['evening']['clock']=='23:00' and new['triggers']['evening']['modules']==['evening_briefing']
    assert new['triggers']['subscription_expiries']['mode']=='subscription'
    assert new['module_enabled']['evening_briefing'] and new['module_enabled']['subscription_reminder']
def main():
    ap=argparse.ArgumentParser()
    for n in ['binary','sha256','source','source-sha256','readme']:ap.add_argument('--'+n,required=True)
    ap.add_argument('--apply',action='store_true');a=ap.parse_args()
    if not a.apply:print(json.dumps({'review_only':True,'version':VERSION}));return
    assert os.geteuid()==0 and os.readlink('/opt/mybot/current')==PREVIOUS
    assert sha('/opt/mybot/current/reasonix')==PREVIOUS_SHA
    prompt=HOME/'prompts/friday.md';assert sha(prompt)==PROMPT_SHA
    assert sha(a.binary)==a.sha256 and sha(a.source)==a.source_sha256
    assert run(a.binary,'--version').strip()=='reasonix '+VERSION
    release=Path('/opt/mybot/releases')/VERSION;assert not release.exists();release.mkdir(mode=0o755)
    source=release/'source';source.mkdir()
    with tarfile.open(a.source) as tf:
        assert all(m.isfile() and not Path(m.name).is_absolute() and '..' not in Path(m.name).parts for m in tf.getmembers())
        tf.extractall(source)
    manifest=json.loads((source/'SOURCE-MANIFEST.json').read_text())
    assert all(sha(source/name)==value for name,value in manifest['files'].items())
    old_source=Path(PREVIOUS)/'source'
    old_go={str(p.relative_to(old_source)) for p in old_source.rglob('*.go')}
    new_go={str(p.relative_to(source)) for p in source.rglob('*.go')}
    assert old_go<=new_go,'unexpected Go source removal'
    changed_go={p for p in old_go if sha(old_source/p)!=sha(source/p)}|(new_go-old_go)
    assert changed_go<=set(manifest['reviewed_go_changes']),'unexpected compiled source delta'
    shutil.copyfile(a.binary,release/'reasonix');(release/'reasonix').chmod(0o755)
    protected=[HOME/'.env',HOME/'config.toml',HOME/'assistant/mail.json',HOME/'assistant/weather.json',HOME/'weixin/accounts/default.json']+sorted((HOME/'assistant/memory').glob('*.md'))
    fingerprints={str(p):sha(p) for p in protected}
    backup=Path('/var/lib/mybot/backups')/('six-features-'+dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ'));backup.mkdir(mode=0o700)
    readme=Path('/home/ubuntu/Project/MyBot/README.md');shutil.copyfile(prompt,backup/'friday.md');shutil.copyfile(readme,backup/'README.md');(backup/'friday.md').chmod(0o600)
    database=HOME/'assistant/state.sqlite3';account=pwd.getpwnam('mybot');migrated=None
    run('systemctl','stop',SERVICE)
    try:
        with sqlite3.connect(database) as src,sqlite3.connect(backup/'state.sqlite3') as dst:src.backup(dst)
        (backup/'state.sqlite3').chmod(0o600)
        with sqlite3.connect(database) as db:before=entries(db)
        old=json.loads(before[('heartbeat_config','default')]);assert 'evening' not in old['triggers'] and 'subscription_expiries' not in old['triggers']
        assert fingerprints=={str(p):sha(p) for p in protected}
        temp=prompt.with_name('friday.md.six-next');shutil.copyfile(source/'deploy/tencent/friday.md',temp);temp.chmod(0o600);os.chown(temp,account.pw_uid,account.pw_gid);os.replace(temp,prompt)
        switch(str(release));shutil.copyfile(a.readme,readme)
        migrated=json.loads(run('runuser','-u','mybot','--','env','HOME=/var/lib/mybot','REASONIX_HOME='+str(HOME),'REASONIX_ASSISTANT=1',str(release/'reasonix'),'assistant','expansion-enable',cwd='/var/lib/mybot/workspace'))
        verify_config(old,migrated)
        with sqlite3.connect(database) as db:after=entries(db)
        changed={key for key in set(before)|set(after) if before.get(key)!=after.get(key)}
        assert changed=={('heartbeat_config','default'),('assistant_policy','expansion_v1')},'unexpected personal state mutation'
        assert fingerprints=={str(p):sha(p) for p in protected}
        since=str(int(time.time()));run('systemctl','start',SERVICE)
        for _ in range(40):
            journal=run('journalctl','-u',SERVICE,'--since','@'+since,'--no-pager','-o','cat')
            if run('systemctl','is-active',SERVICE).strip()=='active' and 'weixin getupdates ready' in journal:break
            time.sleep(1)
        else:raise RuntimeError('Weixin readiness not confirmed')
        pid=run('systemctl','show',SERVICE,'-p','MainPID','--value').strip();assert sha('/proc/'+pid+'/exe')==a.sha256
        with sqlite3.connect(database) as db:
            current=json.loads(db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0]);verify_config(old,current)
            assert current['revision']==migrated['revision']
            assert db.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
        result={'passed':True,'version':VERSION,'previous_version':Path(PREVIOUS).name,'binary_sha256':a.sha256,'prompt_sha256':sha(prompt),'source_sha256':a.source_sha256,'source_file_count':len(manifest['files']),'changed_go_files':sorted(changed_go),'service_active':True,'weixin_ready':True,'routine_and_two_new_triggers_verified':True,'previous_triggers_and_date_overrides_preserved':True,'all_other_personal_database_rows_preserved_at_activation':True,'protected_configuration_and_memory_files_preserved':True,'explicit_test_messages_sent':0,'backup':str(backup),'heartbeat_revision':current['revision']}
        (release/'BUILD.json').write_text(json.dumps(result,indent=2));print(json.dumps(result,indent=2))
    except Exception:
        run('systemctl','stop',SERVICE)
        # Restore only our configuration if its revision is still ours. Never
        # restore the entire backup DB after live user state could have changed.
        with sqlite3.connect(database) as db:
            current=json.loads(db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0])
            if migrated is not None and current['revision']!=migrated['revision']:
                run('systemctl','start',SERVICE);raise RuntimeError('rollback requires merging newer user routine changes; new binary retained')
            if 'before' in locals():
                db.execute("UPDATE assistant_entities SET payload=? WHERE kind='heartbeat_config' AND id='default'",(before[('heartbeat_config','default')],))
                for ident,payload in db.execute("SELECT id,payload FROM assistant_entities WHERE kind='job'").fetchall():
                    job=json.loads(payload)
                    if job.get('module_id') in ['evening_briefing','subscription_reminder'] or ident.startswith('deadline-'):
                        db.execute("INSERT OR REPLACE INTO assistant_entities(kind,id,payload) VALUES('expansion_rollback_job_v1',?,?)",(ident,payload));db.execute("DELETE FROM assistant_entities WHERE kind='job' AND id=?",(ident,))
        switch(PREVIOUS);shutil.copyfile(backup/'friday.md',prompt);prompt.chmod(0o600);os.chown(prompt,account.pw_uid,account.pw_gid);shutil.copyfile(backup/'README.md',readme);run('systemctl','start',SERVICE);raise
if __name__=='__main__':main()
