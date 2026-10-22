#!/usr/bin/env python3
"""Server-only real-provider acceptance. Run as root; never exports credentials."""
import datetime as dt,json,os,pwd,re,shutil,sqlite3,subprocess,tempfile,time,threading,urllib.request
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from pathlib import Path

def main():
 production=Path("/var/lib/mybot/reasonix")
 binary=Path("/home/ubuntu/Project/MyBot/reasonix/bin/reasonix-linux-amd64")
 account=pwd.getpwnam("mybot")
 with tempfile.TemporaryDirectory(prefix="assistant-live-acceptance-",dir="/var/lib/mybot") as tmp:
  root=Path(tmp);home=root/"home";work=root/"work";home.mkdir();work.mkdir()
  for p in [root,home,work]:p.chmod(0o700);os.chown(p,account.pw_uid,account.pw_gid)
  executable=root/"reasonix";shutil.copyfile(binary,executable);executable.chmod(0o700);os.chown(executable,account.pw_uid,account.pw_gid);binary=executable
  for name in ["config.toml",".env"]:
   dest=home/name;shutil.copyfile(production/name,dest);dest.chmod(0o600);os.chown(dest,account.pw_uid,account.pw_gid)
  path=home/"config.toml";config=path.read_text()
  import tomllib
  if not {'assistant_tasks','assistant_memory'}.issubset(tomllib.loads(config).get('permissions',{}).get('allow',[])):
   config=config.replace('[permissions]\n','[permissions]\nallow = ["assistant_tasks", "assistant_memory"]\n',1)
  upstream=next(v["base_url"] for v in tomllib.loads(config)["providers"] if v["name"]=="ali-medium").rstrip("/")
  wire=[]
  class Proxy(BaseHTTPRequestHandler):
   def log_message(self,*args):pass
   def do_POST(self):
    data=self.rfile.read(int(self.headers.get("Content-Length",0)))
    wire.append(json.loads(data))
    headers={k:v for k,v in self.headers.items() if k.lower() not in ["host","content-length","connection","accept-encoding"]}
    request=urllib.request.Request(upstream+"/chat/completions",data=data,headers=headers,method="POST")
    with urllib.request.urlopen(request,timeout=180) as response:
     answer=response.read();self.send_response(response.status);self.send_header("Content-Type",response.headers.get("Content-Type","application/json"));self.send_header("Content-Length",str(len(answer)));self.end_headers();self.wfile.write(answer)
  proxy=ThreadingHTTPServer(("127.0.0.1",0),Proxy);threading.Thread(target=proxy.serve_forever,daemon=True).start()
  config=config.replace(upstream,"http://127.0.0.1:"+str(proxy.server_port)+"/v1")
  path.write_text(config)
  end=(dt.datetime.now(dt.timezone(dt.timedelta(hours=8)))+dt.timedelta(seconds=30)).isoformat(timespec="seconds")
  base=["sudo","-n","-u","mybot","env","HOME="+str(home),"REASONIX_HOME="+str(home),"REASONIX_ASSISTANT=1","TZ=Asia/Shanghai","REASONIX_TELEMETRY=off",str(binary),"run","--output-format","json","--permission-mode","read-only","--dir",str(work),"--max-steps","12"]
  prompt=f"这是隔离环境的功能验收，请实际调用 assistant_tasks 和 assistant_memory 完成三项持久保存：1. 创建 kind=todo 普通待办，title='验收_整理学习计划'，不安排提醒。2. 创建 kind=long 长期记忆，subject='验收_长期偏好'，body='验收时用简短中文回复'。3. 创建 kind=short 短期记忆，subject='验收_旅行'，body='验收时正在日本旅行'，expires_at='{end}'。三个都要实际保存成功，不要使用 shell 或其他写工具。完成后简单确认。"
  started=time.time();result=subprocess.run(base+["--",prompt],capture_output=True,text=True,timeout=240,cwd=work)
  if result.returncode:raise RuntimeError("real provider run failed; exit="+str(result.returncode))
  envelope=json.loads(result.stdout)
  if envelope.get("is_error"):raise RuntimeError("real provider returned error")
  db=sqlite3.connect(home/"assistant"/"state.sqlite3")
  tasks=[json.loads(row[0]) for row in db.execute("SELECT payload FROM assistant_entities WHERE kind='todo'")]
  jobs=db.execute("SELECT COUNT(*) FROM assistant_entities WHERE kind='job'").fetchone()[0];db.close()
  notes=list((home/"assistant"/"memory").glob("*.md"))
  if len(tasks)!=1 or tasks[0]["title"]!="验收_整理学习计划" or jobs!=0 or len(notes)!=2:raise RuntimeError("real model did not persist exactly one unscheduled todo and two memories")
  for p in notes:
   meta,body=p.read_text().split("\n---\n",1);value=json.loads(meta.removeprefix("---\n"))
   if value["kind"]=="short":
    if os.environ.get("MYBOT_LIVE_FORCE_EXPIRY")=="1":
     value["expires_at"]=(dt.datetime.now(dt.timezone.utc)-dt.timedelta(seconds=1)).isoformat()
     p.write_text("---\n"+json.dumps(value,ensure_ascii=False)+"\n---\n"+body)
    else:
     time.sleep(max(0,dt.datetime.fromisoformat(value["expires_at"].replace("Z","+00:00")).timestamp()-time.time()+.2))
  query="请根据本回合宿主最新的 personal_assistant_current_state 判断，忽略旧对话中过去有效的情况。当前验收_长期偏好是否仍有效、验收_旅行短期记忆是否仍有效、验收_整理学习计划是否仍未完成？不要执行任何写操作，仅返回严格JSON对象，字段为 long_active、short_active、todo_open，值都是布尔值。"
  manifests=[p for p in home.rglob("manifest.json") if p.parent.parent.name=="sessions-v4"]
  if len(manifests)!=1:raise RuntimeError("expected exactly one isolated native conversation")
  session_id=envelope.get("session_id") or json.loads(manifests[0].read_text())["sessionId"]
  second=subprocess.run(base+["--resume",session_id,"--",query],capture_output=True,text=True,timeout=240,cwd=work)
  if second.returncode:raise RuntimeError("real model reused conversation failed")
  answer=json.loads(second.stdout)["result"].strip()
  if answer.startswith("```"):answer=answer.split("\n",1)[1].rsplit("```",1)[0].strip()
  state=json.loads(answer)
  snapshots=[]
  for body in wire:
   for message in body.get("messages",[]):
    content=message.get("content","")
    if not isinstance(content,str):content=json.dumps(content,ensure_ascii=False)
    marker="<personal_assistant_current_state>"
    if marker in content:
     text=content[content.rfind(marker):]
     match=re.search(r"\n({.*})\n</personal_assistant_current_state>",text,re.S)
     current=json.loads(match.group(1)) if match else {}
     snapshots.append({"request":wire.index(body),"role":message.get("role"),"short_active":any(v["subject"]=="验收_旅行" for v in current.get("active_memories",[])),"long_active":any(v["subject"]=="验收_长期偏好" for v in current.get("active_memories",[])),"short_expired_status":any(v["subject"]=="验收_旅行" and v["status"]=="expired" for v in current.get("inactive_memory_status",[]))})
  if not snapshots or snapshots[-1]["short_active"] or not snapshots[-1]["long_active"] or not snapshots[-1]["short_expired_status"]:raise RuntimeError("latest provider request did not receive correct active/inactive memory snapshot")
  print(json.dumps({"wire_requests":len(wire),"snapshot_diagnostics":snapshots,"observed_state":state},ensure_ascii=False))
  if state!={"long_active":True,"short_active":False,"todo_open":True}:raise RuntimeError("real model did not respect fresh expiry context: "+json.dumps(state))
  print(json.dumps({"passed":True,"default_preset":"Medium","expiry_mode":"cross-session-update" if os.environ.get("MYBOT_LIVE_FORCE_EXPIRY")=="1" else "natural","real_provider_tool_persistence":True,"real_model_reused_conversation_expiry":True,"todos":len(tasks),"memories":len(notes),"notification_jobs":jobs,"elapsed_seconds":round(time.time()-started,2),"production_personal_state_modified":False},indent=2))
if __name__=="__main__":main()

