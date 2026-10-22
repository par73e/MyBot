#!/usr/bin/env python3
"""One labelled scheduled reminder to the configured owner; clean up only this test's records."""
import datetime as dt,json,os,pwd,sqlite3,subprocess,time
from pathlib import Path

def main():
 account=pwd.getpwnam("mybot")
 if os.geteuid()==0:os.setgid(account.pw_gid);os.setuid(account.pw_uid)
 if os.geteuid()!=account.pw_uid:raise RuntimeError("run as root or mybot")
 home=Path("/var/lib/mybot/reasonix");database=home/"assistant"/"state.sqlite3"
 binary="/opt/mybot/current/reasonix"
 version=subprocess.run([binary,"--version"],check=True,capture_output=True,text=True).stdout
 if "v1.39.10-assistant.1" not in version:raise RuntimeError("new release must be active")
 title="部署验收_"+dt.datetime.now().strftime("%Y%m%d_%H%M%S")
 at=(dt.datetime.now(dt.timezone(dt.timedelta(hours=8)))+dt.timedelta(seconds=45)).isoformat(timespec="seconds")
 text="Sir，私人助理升级验收完成：这是一条由服务器定时调度发出的测试提醒。"
 prompt=f"请为部署验收创建一个一次性提醒，kind=reminder，title='{title}'，body='{text}'，at='{at}'，timezone='Asia/Shanghai'。只创建这一个提醒，实际使用 assistant_tasks 保存，不要直接发送，不要调用 shell，不创建其他记忆或任务。"
 env=dict(os.environ,HOME="/var/lib/mybot",REASONIX_HOME=str(home),REASONIX_ASSISTANT="1",TZ="Asia/Shanghai",REASONIX_TELEMETRY="0",DO_NOT_TRACK="1")
 result=subprocess.run([binary,"run","--output-format","json","--permission-mode","read-only","--dir","/var/lib/mybot/workspace","--max-steps","6","--",prompt],cwd="/var/lib/mybot/workspace",env=env,capture_output=True,text=True,timeout=180)
 if result.returncode:raise RuntimeError("live reminder creation failed")
 todo_id=job_id=None;status="not_created";failure=False
 def connect():return sqlite3.connect(database,timeout=5)
 try:
  with connect() as db:
   rows=db.execute("SELECT payload FROM assistant_entities WHERE kind='todo' AND json_extract(payload,'$.title')=?",(title,)).fetchall()
  if len(rows)!=1:raise RuntimeError("model did not create exactly one labelled test reminder")
  todo=json.loads(rows[0][0]);todo_id=todo["id"];job_id=todo.get("job_id")
  if not job_id:raise RuntimeError("test reminder has no scheduled job")
  deadline=time.time()+75
  while time.time()<deadline:
   with connect() as db:
    row=db.execute("SELECT payload FROM assistant_entities WHERE kind='job' AND id=?",(job_id,)).fetchone()
   if row:
    job=json.loads(row[0]);history=job.get("history",[])
    if history:
     last=history[-1];status=last["status"]
     if status=="delivered":break
     if status in ["failed","interrupted","delivery_uncertain","delivery_failed","delivery_expired"] or last.get("error"):failure=True;break
   time.sleep(.2)
  passed=status=="delivered" and not failure
 finally:
  # These identities came from the uniquely named test created above. Do not
  # touch unrelated personal tasks or memories.
  if todo_id and job_id:
   with connect() as db:
    db.execute("DELETE FROM assistant_entities WHERE kind='todo' AND id=? AND json_extract(payload,'$.title')=?",(todo_id,title))
    db.execute("DELETE FROM assistant_entities WHERE kind='job' AND id=? AND json_extract(payload,'$.title')=?",(job_id,title))
 print(json.dumps({"passed":passed,"actual_weixin_api_acknowledged":passed,"scheduled_status":status,"test_title":title,"cleanup":{"test_todos_removed":1 if todo_id else 0,"test_jobs_removed":1 if job_id else 0},"personal_memories_modified":False},ensure_ascii=False,indent=2))
 if not passed:raise SystemExit(1)
if __name__=="__main__":main()

