#!/usr/bin/env python3
"""Real bot/child-Agent/tool/SQLite/Weixin acceptance with simulated endpoints.
Requires a TEST binary built with the mail-fixture-transport.py embed overlay.
No real credentials/accounts/providers are used. Does not alter production.
"""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import queue
import signal
import sqlite3
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    ap=argparse.ArgumentParser();ap.add_argument('binary');args=ap.parse_args()
    with tempfile.TemporaryDirectory(prefix='mybot-mail-acceptance-') as tmp:
        root=Path(tmp);home=root/'home';work=root/'work';home.mkdir();work.mkdir();(home/'assistant').mkdir()
        incoming=queue.Queue();sent=[];requests=[];errors=[];reads=[];background=[]
        accounts=[];files={}
        for key,provider,address in [('qq','qq','owner@qq.com'),('gmail1','gmail','first@gmail.com'),('gmail2','gmail','second@gmail.com')]:
            p=root/(key+'.json');p.write_text(json.dumps({'messages':[]}));files[key]=p
            a={'id':key,'provider':provider,'address':address,'credential_file':str(p)}
            if key=='qq':a['forwarded_accounts']=[{'id':'outlook','address':'owner@outlook.com'}]
            accounts.append(a)
        (home/'assistant'/'mail.json').write_text(json.dumps({'enabled':True,'poll_seconds':60,'accounts':accounts}))
        def add(key,seq,subject,body,to=None):
            d=json.loads(files[key].read_text());d['messages'].append({'sequence':seq,'transport_id':str(seq),'message_id':f'<{key}-{seq}@fixture>','from':'sender@example.com','to':to or next(a['address'] for a in accounts if a['id']==key),'subject':subject,'received_at':int(time.time()),'body':body})
            temp=files[key].with_suffix('.next');temp.write_text(json.dumps(d));temp.replace(files[key])
        class Handler(BaseHTTPRequestHandler):
            def log_message(self,*a):pass
            def reply(self,obj):
                raw=json.dumps(obj).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers()
                try:self.wfile.write(raw)
                except (BrokenPipeError,ConnectionResetError):pass
            def do_POST(self):
                body=json.loads(self.rfile.read(int(self.headers.get('Content-Length',0))) or b'{}')
                if self.path.endswith('/getupdates'):
                    try:msgs=[incoming.get(timeout=.1)]
                    except queue.Empty:msgs=[]
                    return self.reply({'ret':0,'errcode':0,'msgs':msgs,'get_updates_buf':'fixture'})
                if self.path.endswith('/sendmessage'):
                    sent.append(body);return self.reply({'ret':0,'errcode':0,'message_id':str(len(sent))})
                if '/ilink/' in self.path:return self.reply({'ret':0,'errcode':0,'typing_ticket':'fixture'})
                if not self.path.endswith('/chat/completions'):return self.reply({'error':'unexpected path'})
                requests.append(body);messages=body.get('messages',[])
                rendered=json.dumps(messages,ensure_ascii=False)
                is_bg='[Host notification contract]' in rendered
                tools={t['function']['name'] for t in body.get('tools',[])}
                if 'assistant_mail' not in tools:errors.append('mail tool missing from model surface')
                if is_bg and tools-{'assistant_mail','assistant_tasks','assistant_memory','assistant_history'}:errors.append('unsafe background tools advertised')
                tool_messages=[m for m in messages if m.get('role')=='tool']
                calls=[];last=tool_messages[-1].get('content','') if tool_messages else ''
                if not is_bg:
                    # Follow-up tool state only since latest user request, excluding background references.
                    last_user=max((i for i,m in enumerate(messages) if m.get('role')=='user'),default=0)
                    tool_messages=[m for m in messages[last_user+1:] if m.get('role')=='tool']
                    last=tool_messages[-1].get('content','') if tool_messages else ''
                if is_bg:
                    contract=next(str(m.get('content','')) for m in messages if '[Host notification contract]' in str(m.get('content','')))
                    marker='New email IDs (DATA): '
                    ids=json.JSONDecoder().raw_decode(contract.split(marker,1)[1])[0]
                    seen=[]
                    for m in tool_messages:
                        try:v=json.loads(m.get('content',''))
                        except (ValueError,TypeError):continue
                        if isinstance(v,dict) and 'body' in v:seen.append(v['message']['id'])
                    missing=[v for v in ids if v not in seen]
                    if missing:calls=[('assistant_mail',{'action':'read','id':missing[0]})]
                    else:
                        notify='IMPORTANT_BODY' in rendered
                        final=json.dumps({'notify':notify,'message':'MAIL_IMPORTANT_NOTICE' if notify else '', 'reason':'action needed' if notify else 'no interruption needed'})
                        background.append(notify)
                elif 'MAIL_SCHEDULE_OWNER_TEST' in str(messages[last_user].get('content','')):
                    if 'assistant_mail_schedule' not in tools:errors.append('schedule control tool missing')
                    if not tool_messages:calls=[('assistant_mail_schedule',{'action':'get'})]
                    elif '"revision":1' in last or '"revision": 1' in last:
                        calls=[('assistant_mail_schedule',{'action':'update','revision':1,'interval_seconds':60,'weeklySchedule':{'monday': {'enabled': True, 'start': '10:00', 'end': '24:00'}, 'tuesday': {'enabled': True, 'start': '08:00', 'end': '24:00'}, 'wednesday': {'enabled': True, 'start': '10:00', 'end': '24:00'}, 'thursday': {'enabled': True, 'start': '10:00', 'end': '24:00'}, 'friday': {'enabled': True, 'start': '08:00', 'end': '24:00'}, 'saturday': {'enabled': True, 'start': '11:00', 'end': '24:00'}, 'sunday': {'enabled': True, 'start': '11:00', 'end': '24:00'}},'timezone':'Asia/Shanghai'})]
                    else:
                        v=json.loads(last)
                        if v.get('revision')!=2 or v.get('weeklySchedule',{}).get('saturday',{}).get('start')!='11:00':errors.append('schedule update did not persist')
                        final='MAIL_SCHEDULE_UPDATED'
                elif not tool_messages:
                    calls=[('assistant_mail',{'action':'list'})]
                elif '"messages"' in last:
                    v=json.loads(last);items=v.get('messages',[])
                    if not items:errors.append('foreground list is empty');final='EMPTY'
                    else:calls=[('assistant_mail',{'action':'read','id':items[0]['id']})]
                else:
                    if 'FOREGROUND_BODY' not in last:errors.append('foreground did not read body')
                    reads.append(last);final='MAIL_FOREGROUND_SUMMARY'
                if calls:
                    delta={'tool_calls':[{'index':i,'id':f'call-{len(requests)}-{i}','type':'function','function':{'name':n,'arguments':json.dumps(a)}} for i,(n,a) in enumerate(calls)]};finish='tool_calls'
                else:delta={'content':final};finish='stop'
                data=('data: '+json.dumps({'choices':[{'delta':delta,'finish_reason':finish}]})+'\n\ndata: [DONE]\n\n').encode()
                self.send_response(200);self.send_header('Content-Type','text/event-stream');self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
        server=ThreadingHTTPServer(('127.0.0.1',0),Handler);threading.Thread(target=server.serve_forever,daemon=True).start();base='http://127.0.0.1:'+str(server.server_port)
        (home/'config.toml').write_text(f'''
default_model = "fixture/model"
[agent]
temperature = 0.0
[permissions]
mode = "ask"
allow = ["assistant_mail", "assistant_tasks", "assistant_memory", "assistant_mail_schedule"]
[[providers]]
name = "fixture"
kind = "openai"
base_url = "{base}/v1"
models = ["model"]
default = "model"
api_key_env = "MAIL_FIXTURE_KEY"
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
token_env = "MAIL_FIXTURE_WEIXIN"
api_base = "{base}"
''')
        env=dict(os.environ,REASONIX_HOME=str(home),HOME=str(home),REASONIX_ASSISTANT='1',TZ='Asia/Shanghai',MAIL_FIXTURE_KEY='fixture',MAIL_FIXTURE_WEIXIN='fixture',REASONIX_TELEMETRY='off')
        command=[str(Path(args.binary).resolve()),'bot','start','--channels','weixin','--dir',str(work),'--heartbeat','--heartbeat-poll','1s']
        log=open(root/'bot.log','w+');process=None
        def wait_for(predicate,seconds=20):
            deadline=time.monotonic()+seconds
            while time.monotonic()<deadline:
                if predicate():return
                if process.poll() is not None:raise RuntimeError('bot exited')
                time.sleep(.2)
            raise RuntimeError('acceptance timed out')
        def dbrows(kind):
            p=home/'assistant'/'state.sqlite3'
            if not p.exists():return []
            with sqlite3.connect(p) as db:return [json.loads(r[0]) for r in db.execute('SELECT payload FROM assistant_entities WHERE kind=?',(kind,))]
        try:
            # Real minute scheduler, not an accelerated test-only interval.
            process=subprocess.Popen(command,cwd=work,env=env,stdout=log,stderr=log,start_new_session=True)
            wait_for(lambda:len(dbrows('mail_cursor'))==3)
            time.sleep(2)
            if requests:errors.append('empty baseline invoked model')
            print('PASS empty unified check: zero model calls',flush=True)
            # Foreground mail is inserted after baseline; querying must NOT consume its event.
            add('qq',1,'Outlook test','FOREGROUND_BODY IMPORTANT_BODY','owner@outlook.com')
            incoming.put({'message_id':'in-1','from_user_id':'fixture-owner','to_user_id':'fixture','msg_type':1,'context_token':'fixture-context','item_list':[{'type':1,'text_item':{'text':'请看一下我的邮件并概括。'}}]})
            wait_for(lambda:'MAIL_FOREGROUND_SUMMARY' in json.dumps(sent))
            print('PASS foreground: actual child tool list/read and summary',flush=True)
            wait_for(lambda:True in background,80)
            wait_for(lambda:'MAIL_IMPORTANT_NOTICE' in json.dumps(sent))
            print('PASS 60-second poll: new mail -> Agent read -> important push',flush=True)
            add('gmail2',1,'Newsletter','NOISE_BODY')
            wait_for(lambda:False in background,80)
            print('PASS Gmail new mail analyzed and kept silent',flush=True)
            incoming.put({'message_id':'in-2','from_user_id':'fixture-owner','to_user_id':'fixture','msg_type':1,'context_token':'fixture-context','item_list':[{'type':1,'text_item':{'text':'MAIL_SCHEDULE_OWNER_TEST 周二周五8点、周一周三周四10点、周六周日11点开启，24点关闭。'}}]})
            wait_for(lambda:'MAIL_SCHEDULE_UPDATED' in json.dumps(sent))
            monitor=next(j for j in dbrows('job') if j.get('id')=='mail-check')
            if monitor.get('revision')!=2 or monitor.get('weeklySchedule',{}).get('monday',{}).get('start')!='10:00':errors.append('unified heartbeat monitor not updated')
            print('PASS owner Agent updates persisted weekly Heartbeat hours through tool',flush=True)
            time.sleep(2)
            os.killpg(process.pid,signal.SIGTERM);process.wait(timeout=15)
            before=(len(requests),len(sent));process=subprocess.Popen(command,cwd=work,env=env,stdout=log,stderr=log,start_new_session=True)
            time.sleep(4)
            if before!=(len(requests),len(sent)):errors.append('restart repeated mail analysis/delivery')
            monitor=next(j for j in dbrows('job') if j.get('id')=='mail-check')
            if monitor.get('revision')!=2 or monitor.get('weeklySchedule',{}).get('monday',{}).get('start')!='10:00':errors.append('restart reset running hours')
            jobs=[j for j in dbrows('job') if j.get('capability')=='mail']
            states=sorted(j['history'][-1]['status'] for j in jobs)
            if states!=['completed','delivered']:errors.append('mail job states '+str(states))
            if len(reads)!=1 or background!=[True,False]:errors.append('unexpected analysis sequence')
            if json.dumps(sent).count('MAIL_IMPORTANT_NOTICE') != 1:errors.append('important mail notification duplicated or missing')
            if 'notify\\\"' in json.dumps(sent):errors.append('decision envelope leaked to chat')
            out={'passed':not errors,'real_binary':True,'simulated_mail_model_weixin':True,'poll_seconds':60,'sources':4,'direct_connections':3,'foreground_body_reads':len(reads),'background_decisions':background,'model_requests':len(requests),'outbound_messages':len(sent),'owner_schedule_tool':True,'restart_dedup':before==(len(requests),len(sent)),'errors':errors}
            print(json.dumps(out,indent=2))
            if errors:raise RuntimeError('acceptance failed')
        except Exception:
            log.flush();log.seek(0);print(log.read()[-12000:]);raise
        finally:
            if process and process.poll() is None:os.killpg(process.pid,signal.SIGTERM);process.wait(timeout=15)
            server.shutdown();log.close()

if __name__=='__main__':main()
