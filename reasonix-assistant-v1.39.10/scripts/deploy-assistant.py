#!/usr/bin/env python3
"""Install an already-tested single-owner release; preserve credentials and rollback on failed health."""
import datetime as dt,hashlib,json,os,pwd,shutil,subprocess,time,tomllib
from pathlib import Path

ROOT=Path("/home/ubuntu/Project/MyBot")
VERSION="v1.39.10-assistant.1"

def command(*args):return subprocess.run(args,check=True,capture_output=True,text=True).stdout
def atomic(path,body,mode,uid=0,gid=0):
 temporary=path.with_name(path.name+".assistant-next")
 temporary.write_bytes(body);temporary.chmod(mode);os.chown(temporary,uid,gid);os.replace(temporary,path)
def pointer(path,target):
 temporary=path.with_name(path.name+".assistant-next")
 if temporary.is_symlink():temporary.unlink()
 temporary.symlink_to(target);os.replace(temporary,path)

def main():
 if os.geteuid()!=0:raise RuntimeError("run with sudo")
 for name in ["full-test-final","build-final","golangci"]:
  if (ROOT/(name+".exit")).read_text().strip()!="0":raise RuntimeError("required gate failed: "+name)
 binary=ROOT/"reasonix"/"bin"/"reasonix-linux-amd64"
 if VERSION not in command(str(binary),"--version"):raise RuntimeError("binary version mismatch")
 user=pwd.getpwnam("mybot");home=Path("/var/lib/mybot/reasonix");config=home/"config.toml"
 old=config.read_bytes();cfg=tomllib.loads(old.decode())
 if cfg["default_model"]!="ali-medium/qwen3.8-flash" or cfg["bot"]["model"]!="ali-medium/qwen3.8-flash":raise RuntimeError("unexpected default model")
 if len(cfg["bot"]["allowlist"]["weixin_users"])!=1:raise RuntimeError("this release requires one Weixin owner")
 credential_paths=[home/".env",home/"weixin"/"accounts"/"default.json"]
 before={str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in credential_paths}
 current=Path("/opt/mybot/current");old_target=os.readlink(current)
 helper=Path("/usr/local/bin/mybot");old_helper=helper.read_bytes()
 dropin=Path("/etc/systemd/system/mybot-heartbeat.service.d/20-assistant.conf")
 previous_dropin=dropin.read_bytes() if dropin.exists() else None
 stamp=dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
 backup=Path("/var/lib/mybot/backups")/("assistant-upgrade-"+stamp);backup.mkdir(parents=True,mode=0o700);backup.chmod(0o700)
 for name,body in [("config.toml",old),("mybot-helper",old_helper),("previous-release.txt",old_target.encode())]:
  p=backup/name;p.write_bytes(body);p.chmod(0o600)
 release=Path("/opt/mybot/releases")/VERSION;release.mkdir(exist_ok=True)
 shutil.copyfile(binary,release/"reasonix");(release/"reasonix").chmod(0o755)
 for name in ["ASSISTANT.md","TEST_REPORT.md"]:
  source=ROOT/"reasonix"/name
  if source.exists():shutil.copyfile(source,release/name)
 allowed=list(dict.fromkeys(cfg.get("permissions",{}).get("allow",[])+["assistant_tasks","assistant_memory"]))
 body=old.decode()
 if cfg.get("permissions",{}).get("allow"):raise RuntimeError("review existing permission allow rules before replacing")
 body=body.replace("[permissions]\n","[permissions]\nallow = "+json.dumps(allowed)+"\n",1)
 parsed=tomllib.loads(body)
 if parsed["permissions"]["allow"]!=allowed:raise RuntimeError("permission update failed validation")
 updated_helper=old_helper.replace(b"REASONIX_HOME=/var/lib/mybot/reasonix",b"REASONIX_ASSISTANT=1 REASONIX_HOME=/var/lib/mybot/reasonix")
 since=str(int(time.time()))
 try:
  atomic(config,body.encode(),0o600,user.pw_uid,user.pw_gid)
  atomic(helper,updated_helper,0o755)
  atomic(dropin,(ROOT/"reasonix"/"deploy"/"tencent"/"20-assistant.conf").read_bytes(),0o644)
  pointer(current,str(release))
  command("systemctl","daemon-reload");command("systemctl","restart","mybot-heartbeat.service")
  ready=False;deadline=time.time()+50
  while time.time()<deadline:
   state=command("systemctl","is-active","mybot-heartbeat.service").strip()
   journal=command("journalctl","-u","mybot-heartbeat.service","--since","@"+since,"--no-pager","-o","cat")
   if state=="active" and "weixin getupdates ready" in journal:ready=True;break
   time.sleep(1)
  if not ready:raise RuntimeError("new service did not establish a healthy Weixin poll")
  if before!={str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in credential_paths}:raise RuntimeError("credential files changed unexpectedly")
  status=json.loads(command("/usr/local/bin/mybot","assistant","status"))
  if not status["enabled"]:raise RuntimeError("assistant opt-in is not active")
  result={"passed":True,"version":VERSION,"release":str(release),"sha256":hashlib.sha256(binary.read_bytes()).hexdigest(),"weixin_poll_ready":True,"credentials_unchanged":True,"default_model":"ali-medium/qwen3.8-flash","backup":str(backup),"assistant_enabled":status["enabled"]}
  (ROOT/"deployment.json").write_text(json.dumps(result,indent=2)+"\n")
  print(json.dumps(result,indent=2))
 except Exception:
  atomic(config,old,0o600,user.pw_uid,user.pw_gid);atomic(helper,old_helper,0o755)
  if previous_dropin is None:dropin.unlink(missing_ok=True)
  else:atomic(dropin,previous_dropin,0o644)
  pointer(current,old_target);command("systemctl","daemon-reload");command("systemctl","restart","mybot-heartbeat.service")
  raise
if __name__=="__main__":main()

