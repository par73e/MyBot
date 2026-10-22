#!/usr/bin/env python3
"""Actual-binary acceptance with fake OpenAI and fake Weixin endpoints, no credentials."""
import argparse, datetime as dt, json, os, queue, signal, sqlite3, subprocess, tempfile, threading, time
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from pathlib import Path

def main():
 ap=argparse.ArgumentParser();ap.add_argument("binary");args=ap.parse_args()
 with tempfile.TemporaryDirectory(prefix="mybot-assistant-acceptance-") as tmp:
  home=Path(tmp)/"home";work=Path(tmp)/"work";home.mkdir();work.mkdir()
  incoming=queue.Queue();sent=[];requests=[];errors=[]
  created=False;analysis_seen=[];epoch=0
  def stamp(seconds):
   return dt.datetime.fromtimestamp(epoch+seconds,dt.timezone(dt.timedelta(hours=8))).isoformat(timespec="seconds")
  def snapshot(body):
   # Current session-context snapshots replace previous snapshots. Inspect the
   # last explicit host marker, not stale prose/tool results in conversation.
   text=json.dumps(body,ensure_ascii=False)
   marker="<personal_assistant_current_state>"
   return text[text.rfind(marker):] if marker in text else ""
  class Handler(BaseHTTPRequestHandler):
   def log_message(self,*a):pass
   def reply(self,obj):
    data=json.dumps(obj).encode();self.send_response(200);self.send_header("Content-Type","application/json");self.send_header("Content-Length",str(len(data)));self.end_headers()
    try:self.wfile.write(data)
    except (BrokenPipeError,ConnectionResetError):pass
   def do_POST(self):
    nonlocal created
    raw=self.rfile.read(int(self.headers.get("Content-Length",0)));body=json.loads(raw or b"{}")
    if self.path.endswith("/getupdates"):
     try:msgs=[incoming.get(timeout=.2)]
     except queue.Empty:msgs=[]
     return self.reply({"ret":0,"errcode":0,"msgs":msgs,"get_updates_buf":"fixture"})
    if self.path.endswith("/sendmessage"):
     sent.append(body);return self.reply({"ret":0,"errcode":0,"message_id":str(len(sent))})
    if "/ilink/" in self.path:return self.reply({"ret":0,"errcode":0,"typing_ticket":"fixture"})
    if not self.path.endswith("/chat/completions"):return self.reply({"error":"unexpected path "+self.path})
    requests.append(body)
    tools={v.get("function",{}).get("name") for v in body.get("tools",[])}
    rendered=json.dumps(body.get("messages",[]),ensure_ascii=False)
    last_user=next((json.dumps(m,ensure_ascii=False) for m in reversed(body.get("messages",[])) if m.get("role")=="user"),"")
    is_background="[Host notification contract]" in rendered
    if is_background:
     current=snapshot(body);analysis_seen.append(current)
     if "旅行临时标记" in current:errors.append("expired short memory remained in background snapshot")
     if "中文长期标记" not in current:errors.append("long memory missing in background snapshot")
     contract=next(json.dumps(m,ensure_ascii=False) for m in body.get("messages",[]) if "[Host notification contract]" in json.dumps(m,ensure_ascii=False))
     notify="CASE_NOTIFY" in contract
     final=json.dumps({"notify":notify,"message":"ACCEPTANCE_PROACTIVE_NOTICE" if notify else "","reason":"new actionable result" if notify else "nothing meaningful changed"},ensure_ascii=False)
     delta={"content":final};finish="stop"
    elif "CASE_INSPECT" in last_user:
     current=snapshot(body)
     if "旅行临时标记" in current:errors.append("expired short memory remained in reused chat context")
     if "中文长期标记" not in current:errors.append("long memory missing in reused chat context")
     delta={"content":"ACCEPTANCE_REFRESHED"};finish="stop"
    elif not created:
     if not {"assistant_tasks","assistant_memory"}.issubset(tools):errors.append("personal tools not visible in actual model request")
     calls=[
      ("assistant_memory",{"action":"create","kind":"long","subject":"语言","body":"中文长期标记"}),
      ("assistant_memory",{"action":"create","kind":"short","subject":"短期旅行","body":"旅行临时标记","expires_at":stamp(9)}),
      ("assistant_tasks",{"action":"create","title":"固定提醒验收","body":"ACCEPTANCE_FIXED_NOTICE","at":stamp(7)}),
      ("assistant_tasks",{"action":"create","title":"静默分析验收","kind":"automation","body":"CASE_SILENT","at":stamp(12)}),
      ("assistant_tasks",{"action":"create","title":"主动分析验收","kind":"automation","body":"CASE_NOTIFY","at":stamp(16)}),
     ]
     delta={"tool_calls":[{"index":i,"id":"fixture-"+str(i),"type":"function","function":{"name":n,"arguments":json.dumps(a,ensure_ascii=False)}} for i,(n,a) in enumerate(calls)]};finish="tool_calls";created=True
    else:
     for msg in body.get("messages",[]):
      if msg.get("role")=="tool":
       content=str(msg.get("content",""))
       if '"is_error":true' in content or "not allowed" in content.lower() or "denied" in content.lower():errors.append("tool permission failed: "+content[:160])
     delta={"content":"ACCEPTANCE_CREATED"};finish="stop"
    data=("data: "+json.dumps({"choices":[{"delta":delta,"finish_reason":finish}]},ensure_ascii=False)+"\n\ndata: [DONE]\n\n").encode()
    self.send_response(200);self.send_header("Content-Type","text/event-stream");self.send_header("Content-Length",str(len(data)));self.end_headers();self.wfile.write(data)
  server=ThreadingHTTPServer(("127.0.0.1",0),Handler);threading.Thread(target=server.serve_forever,daemon=True).start()
  base="http://127.0.0.1:"+str(server.server_port)
  config=f'''
default_model = "fixture/model"
[agent]
temperature = 0.0
[permissions]
mode = "ask"
allow = ["assistant_tasks", "assistant_memory"]
[[providers]]
name = "fixture"
kind = "openai"
base_url = "{base}/v1"
models = ["model"]
default = "model"
api_key_env = "MYBOT_FIXTURE_KEY"
[bot]
enabled = true
model = "fixture/model"
tool_approval_mode = "read-only"
debounce_ms = 0
[bot.pairing]
enabled = false
[bot.allowlist]
enabled = true
allow_all = false
weixin_users = ["fixture-owner"]
weixin_admins = ["fixture-owner"]
weixin_approvers = ["fixture-owner"]
[bot.weixin]
enabled = true
account_id = "fixture"
token_env = "MYBOT_FIXTURE_WEIXIN"
api_base = "{base}"
'''
  (home/"config.toml").write_text(config)
  env=dict(os.environ,REASONIX_HOME=str(home),HOME=str(home),REASONIX_ASSISTANT="1",TZ="Asia/Shanghai",MYBOT_FIXTURE_KEY="fixture-key",MYBOT_FIXTURE_WEIXIN="fixture-token",REASONIX_TELEMETRY="off")
  epoch=time.time()
  incoming.put({"message_id":"fixture-inbound-1","from_user_id":"fixture-owner","to_user_id":"fixture","msg_type":1,"context_token":"fixture-context","item_list":[{"type":1,"text_item":{"text":"请保存我说的长期和短期偏好，并创建提醒和自动分析任务。CASE_CREATE"}}]})
  log=open(Path(tmp)/"bot.log","w+")
  command=[str(Path(args.binary).resolve()),"bot","start","--channels","weixin","--dir",str(work),"--heartbeat","--heartbeat-poll","1s"]
  process=subprocess.Popen(command,cwd=work,env=env,stdout=log,stderr=log,start_new_session=True)
  try:
   deadline=time.time()+38
   while time.time()<deadline:
    if process.poll() is not None:break
    texts=json.dumps(sent,ensure_ascii=False)
    if "ACCEPTANCE_FIXED_NOTICE" in texts and "ACCEPTANCE_PROACTIVE_NOTICE" in texts and len(analysis_seen)>=2:break
    time.sleep(.2)
   incoming.put({"message_id":"fixture-inbound-2","from_user_id":"fixture-owner","to_user_id":"fixture","msg_type":1,"context_token":"fixture-context","item_list":[{"type":1,"text_item":{"text":"查看当前有效的记忆和待办。CASE_INSPECT"}}]})
   refresh_deadline=time.time()+12
   while time.time()<refresh_deadline and "ACCEPTANCE_REFRESHED" not in json.dumps(sent,ensure_ascii=False):time.sleep(.2)
   if "ACCEPTANCE_REFRESHED" not in json.dumps(sent,ensure_ascii=False):errors.append("reused owner conversation did not refresh")
   # Restart the real bot with the same state. None of these one-shot jobs may
   # execute or send a second time.
   os.killpg(process.pid,signal.SIGTERM);process.wait(timeout=12)
   before_count=len(sent);before_model=len(requests)
   restarted=subprocess.Popen(command,cwd=work,env=env,stdout=log,stderr=log,start_new_session=True)
   time.sleep(3);os.killpg(restarted.pid,signal.SIGTERM);restarted.wait(timeout=12)
   if len(sent)!=before_count or len(requests)!=before_model:errors.append("restart duplicated scheduled execution or delivery")
   texts=json.dumps(sent,ensure_ascii=False)
   if "ACCEPTANCE_FIXED_NOTICE" not in texts:errors.append("fixed reminder not delivered through actual Weixin adapter")
   if "ACCEPTANCE_PROACTIVE_NOTICE" not in texts:errors.append("proactive message not delivered")
   if len(analysis_seen)!=2:errors.append("expected two real child Agent runs, got "+str(len(analysis_seen)))
   if '"notify"' in texts or "nothing meaningful changed" in texts:errors.append("decision envelope/silent output leaked to chat")
   database=home/"assistant"/"state.sqlite3"
   if database.exists():
    db=sqlite3.connect(database)
    todos=[json.loads(row[0]) for row in db.execute("SELECT payload FROM assistant_entities WHERE kind='todo'")]
    jobs=[json.loads(row[0]) for row in db.execute("SELECT payload FROM assistant_entities WHERE kind='job'")]
    if len(todos)!=3 or len(jobs)!=3:errors.append(f"expected 3 atomic todos/jobs, got {len(todos)}/{len(jobs)}")
    if any(t["status"]!="open" for t in todos):errors.append("delivery incorrectly completed a todo")
    statuses=sorted(j["history"][-1]["status"] for j in jobs if j.get("history"))
    if statuses!=["completed","delivered","delivered"]:errors.append("unexpected job statuses "+repr(statuses))
   else:errors.append("no assistant database created")
   if len(list((home/"assistant"/"memory").glob("*.md")))!=2:errors.append("memory Markdown files missing")
   result={"passed":not errors,"model_requests":len(requests),"agent_analysis_runs":len(analysis_seen),"weixin_outbound_messages":len(sent),"errors":errors}
   print(json.dumps(result,ensure_ascii=False,indent=2))
   if errors:
    log.seek(0);print(log.read()[-16000:]);raise SystemExit(1)
  finally:
   if process.poll() is None:os.killpg(process.pid,signal.SIGKILL);process.wait()
   server.shutdown();log.close()
if __name__=="__main__":main()

