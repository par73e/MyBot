"""Run as root on the owning server. Password arrives as one stdin JSON line."""
import sys, json, tomllib, sqlite3, hashlib, shlex, subprocess, time, os
from pathlib import Path

def main():
    if os.geteuid()!=0: raise SystemExit('root required')
    requested=json.loads(sys.stdin.readline())
    home=Path('/var/lib/mybot/reasonix'); config_path=home/'config.toml'; prompt_path=home/'prompts/friday.md'; credential_path=home/'.env'
    before={str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in (config_path,prompt_path,credential_path)}
    cfg=tomllib.loads(config_path.read_text()); ref=cfg['default_model']; name,model=ref.split('/',1)
    provider=next(p for p in cfg['providers'] if p['name']==name)
    if cfg['bot']['model']!=ref: raise RuntimeError('foreground and bot models differ; review first')
    envname=provider['api_key_env']; key=None
    for line in credential_path.read_text().splitlines():
        stripped=line.strip()
        if stripped.startswith('export '): stripped=stripped[7:]
        if stripped.startswith(envname+'='):
            tokens=shlex.split(stripped.split('=',1)[1],comments=True)
            if len(tokens)!=1: raise RuntimeError('credential format requires explicit parser review')
            key=tokens[0]
    if not key: raise RuntimeError('named model credential not found')
    source=sqlite3.connect('file:'+str(home/'assistant/state.sqlite3')+'?mode=ro',uri=True)
    row=source.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone(); source.close()
    heartbeat=json.loads(row[0])
    source_config={'model':{'model':model,'base_url':provider['base_url'],'effort':provider.get('effort') or provider['default_effort']},'weekly_schedule':heartbeat['weekly_schedule'],'date_overrides':heartbeat['date_overrides'],'prompt':prompt_path.read_text(),'fingerprint':before,'enabled_modules':[k for k,v in heartbeat['module_enabled'].items() if v and k!='subscription_reminder']}
    login=requested.get('login')
    if not isinstance(login,str) or not login or len(login)>128 or any(ch in login for ch in '\x00\r\n'):
        raise ValueError('explicit destination login required')
    payload={'login':login,'password':requested['password'],'model_key':key,'source':source_config}
    args=['docker','exec','-i','-e','ASROBOT_MIGRATION_LOGIN='+login,'asrobot','python','-m','app.migrate']
    preview=subprocess.run(args,input=json.dumps(payload),capture_output=True,text=True,check=True)
    result=json.loads(preview.stdout)
    if result['status']=='already-applied': print(preview.stdout); return
    if result['status']!='preview': raise RuntimeError('preview failed')
    # Consistent target backup and no source credentials in the backup directory.
    backups=Path('/var/backups/asrobot'); backups.mkdir(mode=0o700,parents=True,exist_ok=True)
    destination=backups/('before-friday-config-'+str(int(time.time()))+'.sqlite3')
    target=sqlite3.connect('file:/var/lib/asrobot/platform.sqlite3?mode=ro',uri=True)
    snapshot=sqlite3.connect(destination); target.backup(snapshot); snapshot.close(); target.close(); destination.chmod(0o600)
    if before!={str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in (config_path,prompt_path,credential_path)}: raise RuntimeError('source configuration changed before commit; preview again')
    source=sqlite3.connect('file:'+str(home/'assistant/state.sqlite3')+'?mode=ro',uri=True)
    fresh=json.loads(source.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0]); source.close()
    if any(fresh.get(k)!=heartbeat.get(k) for k in ('weekly_schedule','date_overrides','module_enabled')): raise RuntimeError('source routine changed before commit; preview again')
    applied=subprocess.run(args+['commit'],input=json.dumps(payload),capture_output=True,text=True,check=True)
    unchanged=before=={str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in (config_path,prompt_path,credential_path)}
    print(applied.stdout.strip())
    print(json.dumps({'target_backup':str(destination),'source_files_unchanged':unchanged,'source_service':subprocess.check_output(['systemctl','is-active','mybot-heartbeat'],text=True).strip()},ensure_ascii=False))
if __name__=='__main__': main()
