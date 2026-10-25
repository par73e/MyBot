#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Actual executable, isolated state, mock provider and Weixin; no production credentials."""
import argparse, datetime as dt, json, os, queue, signal, sqlite3, subprocess, tempfile, threading, time
from pathlib import Path
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    ap=argparse.ArgumentParser();ap.add_argument('binary');a=ap.parse_args();binary=str(Path(a.binary).resolve())
    with tempfile.TemporaryDirectory(prefix='mybot-six-acceptance-') as tmp:
        home,work=Path(tmp)/'home',Path(tmp)/'work';home.mkdir();work.mkdir()
        incoming=queue.Queue();sent=[];requests=[];errors=[]
        zone=dt.timezone(dt.timedelta(hours=8));now=dt.datetime.now(zone)
        trigger=(now+dt.timedelta(minutes=1)).replace(second=0,microsecond=0)
        if (trigger-now).total_seconds()<18:trigger+=dt.timedelta(minutes=1)
        assert trigger.date()==now.date(),'run away from midnight'
        stamp=lambda d:d.isoformat(timespec='seconds')
        due=stamp((now+dt.timedelta(days=2)).replace(hour=18,minute=0,second=0,microsecond=0))
        question='「提交报名材料」截止时间已明确，要我记下并提醒吗？'
        fixed_at=stamp(trigger+dt.timedelta(seconds=12))
        class Handler(BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def reply(self,obj):
                data=json.dumps(obj).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(data)));self.end_headers()
                try:self.wfile.write(data)
                except (BrokenPipeError,ConnectionResetError):pass
            def do_POST(self):
                body=json.loads(self.rfile.read(int(self.headers.get('Content-Length',0))) or b'{}')
                if self.path.endswith('/getupdates'):
                    try:msgs=[incoming.get(timeout=.15)]
                    except queue.Empty:msgs=[]
                    return self.reply({'ret':0,'errcode':0,'msgs':msgs,'get_updates_buf':'fixture'})
                if self.path.endswith('/sendmessage'):
                    sent.append(body);return self.reply({'ret':0,'errcode':0,'message_id':str(len(sent))})
                if '/ilink/' in self.path:return self.reply({'ret':0,'errcode':0,'typing_ticket':'fixture'})
                requests.append(body);messages=body.get('messages',[]);rendered=json.dumps(messages,ensure_ascii=False)
                def call(name,args):return {'tool_calls':[{'index':0,'id':'fixture-call','type':'function','function':{'name':name,'arguments':json.dumps(args,ensure_ascii=False)}}]},'tool_calls'
                if '宿主负责把剩余事项与明日最早安排' in rendered:
                    tools={t.get('function',{}).get('name') for t in body.get('tools',[])}
                    if tools-{'assistant_evening','assistant_tasks','assistant_memory','assistant_history'}:errors.append('extra evening tools')
                    if 'LONG_PREF_MARKER' not in rendered or 'ACTIVE_SHORT_MARKER' not in rendered or 'EXPIRED_BODY_MARKER' in rendered:errors.append('memory boundary')
                    if not any(m.get('role')=='tool' and 'remaining_tasks' in str(m.get('content','')) for m in messages):delta,finish=call('assistant_evening',{'action':'snapshot'})
                    else:delta,finish={'content':json.dumps({'care':'今天辛苦了，早点休息，晚安。'},ensure_ascii=False)},'stop'
                else:
                    last=max(i for i,m in enumerate(messages) if m.get('role')=='user')
                    text=messages[last].get('content','');tail=messages[last+1:]
                    if '列出邮件待办建议' in str(text):
                        if not any(m.get('role')=='tool' for m in tail):delta,finish=call('assistant_proposals',{'action':'list'})
                        else:delta,finish={'content':question},'stop'
                    elif '可以，帮我记下' in str(text):
                        if not any(m.get('role')=='tool' for m in tail):delta,finish=call('assistant_proposals',{'action':'approve','id':'proposal-fixture','revision':1})
                        else:
                            if 'reminder_job_ids' not in json.dumps(tail):errors.append('approval failed: '+json.dumps(tail)[-600:])
                            delta,finish={'content':'Sir，已经记下报名材料，并安排截止提醒。'},'stop'
                    elif '设置测试吃饭提醒' in str(text):
                        if not any(m.get('role')=='tool' for m in tail):delta,finish=call('assistant_tasks',{'action':'create','kind':'reminder','title':'吃饭测试','body':'Sir，记得吃饭。','at':fixed_at})
                        else:delta,finish={'content':'Sir，到时提醒你吃饭。'},'stop'
                    else:errors.append('unexpected foreground input');delta,finish={'content':'Sir，在。'},'stop'
                data=('data: '+json.dumps({'choices':[{'delta':delta,'finish_reason':finish}]},ensure_ascii=False)+'\n\ndata: [DONE]\n\n').encode()
                self.send_response(200);self.send_header('Content-Type','text/event-stream');self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
        server=ThreadingHTTPServer(('127.0.0.1',0),Handler);threading.Thread(target=server.serve_forever,daemon=True).start();base='http://127.0.0.1:'+str(server.server_port)
        (home/'config.toml').write_text(f'''default_model="fixture/model"
[permissions]
mode="ask"
allow=["assistant_tasks","assistant_memory","assistant_proposals","assistant_subscriptions","assistant_evening","assistant_heartbeat"]
[[providers]]
name="fixture"
kind="openai"
base_url="{base}/v1"
models=["model"]
default="model"
api_key_env="SIX_FIXTURE_KEY"
[bot]
enabled=true
model="fixture/model"
tool_approval_mode="read-only"
debounce_ms=0
[bot.pairing]
enabled=false
[bot.allowlist]
enabled=true
allow_all=false
weixin_users=["fixture-owner"]
weixin_admins=["fixture-owner"]
weixin_approvers=["fixture-owner"]
[bot.weixin]
enabled=true
account_id="fixture"
token_env="SIX_FIXTURE_WEIXIN"
api_base="{base}"
''')
        env=dict(os.environ,REASONIX_HOME=str(home),HOME=str(home),REASONIX_ASSISTANT='1',TZ='Asia/Shanghai',SIX_FIXTURE_KEY='fixture-key',SIX_FIXTURE_WEIXIN='fixture-token',DO_NOT_TRACK='1')
        def cli(*args):
            p=subprocess.run([binary,'assistant',*args],cwd=work,env=env,capture_output=True,text=True,timeout=20)
            if p.returncode:raise RuntimeError(p.stderr[-2000:])
            return json.loads(p.stdout)
        cli('memory',json.dumps({'action':'create','kind':'long','subject':'pref','body':'LONG_PREF_MARKER'}))
        for n,(body,expiry) in enumerate([('ACTIVE_SHORT_MARKER',trigger+dt.timedelta(hours=1)),('EXPIRED_BODY_MARKER',now+dt.timedelta(seconds=5))]):cli('memory',json.dumps({'action':'create','kind':'short','subject':'temporary'+str(n),'body':body,'starts_at':stamp(now-dt.timedelta(hours=2)),'expires_at':stamp(expiry)}))
        cfg=cli('expansion-enable')
        days=['monday','tuesday','wednesday','thursday','friday','saturday','sunday']
        weekly={d:{'enabled':True,'start':'08:00','end':'24:00'} for d in days};weekly[days[now.weekday()]]['start']=trigger.strftime('%H:%M')
        cfg=cli('heartbeat',json.dumps({'action':'set_weekly','revision':cfg['revision'],'weekly_schedule':weekly}))['config']
        for ident in list(cfg['triggers']):
            req={'action':'set_trigger','revision':cfg['revision'],'trigger_id':ident,'enabled':ident in ['evening','subscription_expiries']}
            if ident=='evening':req['clock']=trigger.strftime('%H:%M')
            cfg=cli('heartbeat',json.dumps(req))['config']
        cli('subscriptions',json.dumps({'action':'create','name':'测试会员','expires_on':str(now.date()+dt.timedelta(days=7))}))
        database=home/'assistant/state.sqlite3'
        source={'id':'mail-source-fixture','title':'source','prompt':'completed source','kind':'agent','enabled':False,'workspaceRoot':str(work),'timezone':'Asia/Shanghai','approvalMode':'read-only','at':stamp(now-dt.timedelta(minutes=1)),'history':[{'startedAt':123,'status':'delivered'}]}
        proposal={'id':'proposal-fixture','revision':1,'candidate':{'title':'提交报名材料','due_at':due,'evidence_quote':'synthetic evidence, extraction verified separately in Go'},'mail_id':'mail-fixture','analysis_job_id':source['id'],'analysis_attempt':123,'state':'pending','question':question,'created_at':stamp(now)}
        with sqlite3.connect(database) as db:
            for kind,ident,value in [('job',source['id'],source),('task_proposal',proposal['id'],proposal)]:db.execute('INSERT INTO assistant_entities(kind,id,payload) VALUES(?,?,?)',(kind,ident,json.dumps(value)))
        log=open(home/'bot.log','w+');command=[binary,'bot','start','--channels','weixin','--dir',str(work),'--heartbeat','--heartbeat-poll','1s'];process=subprocess.Popen(command,cwd=work,env=env,stdout=log,stderr=log,start_new_session=True)
        def send(text,ident):incoming.put({'message_id':ident,'from_user_id':'fixture-owner','to_user_id':'fixture','msg_type':1,'context_token':'fixture','item_list':[{'type':1,'text_item':{'text':text}}]})
        def waitfor(predicate,timeout=30):
            end=time.time()+timeout
            while time.time()<end:
                if predicate():return
                if process.poll() is not None:raise RuntimeError('bot exited')
                time.sleep(.2)
            raise AssertionError('timeout waiting for condition')
        def rows(kind):
            with sqlite3.connect(database) as db:return [json.loads(v[0]) for v in db.execute('SELECT payload FROM assistant_entities WHERE kind=?',(kind,))]
        try:
            send('列出邮件待办建议','ask');waitfor(lambda:question in json.dumps(sent,ensure_ascii=False))
            assert not rows('todo'),'query or proposal wrote todo before confirmation'
            send('可以，帮我记下','confirm');waitfor(lambda:len(rows('todo'))==1)
            tasks=rows('todo');assert len(tasks[0].get('reminder_job_ids',[]))==2,'deadline defaults absent'
            send('设置测试吃饭提醒','remind');waitfor(lambda:len(rows('todo'))==2)
            waitfor(lambda:len([j for j in rows('job') if j.get('module_id') in ['evening_briefing','subscription_reminder'] and j.get('history') and j['history'][-1]['status']=='delivered'])==2,max(40,trigger.timestamp()-time.time()+40))
            waitfor(lambda:'Sir，记得吃饭。' in json.dumps(sent,ensure_ascii=False),25)
            assert len(rows('evening_delivered'))==1,'evening acknowledgement missing'
            assert '明天的课表暂时没查到' in json.dumps(sent,ensure_ascii=False),'missing data hidden'
            assert not errors,errors
            count=len(sent);model_count=len(requests)
            os.killpg(process.pid,signal.SIGTERM);process.wait(timeout=15)
            process=subprocess.Popen(command,cwd=work,env=env,stdout=log,stderr=log,start_new_session=True)
            time.sleep(3);assert len(sent)==count and len(requests)==model_count,'restart replayed'
            assert len(requests)==8,'expected six foreground requests and two evening requests; native reminders must use no model'
            print(json.dumps({'passed':True,'actual_binary':True,'real_weixin_messages':0,'real_model_calls':0,'mock_model_calls':len(requests),'query_before_confirmation_has_no_todo':True,'real_controller_confirmation_created_task_and_two_deadline_jobs':True,'native_subscription_and_fixed_reminder_zero_model':True,'evening_snapshot_and_acknowledgement':True,'effective_long_short_and_expired_memory_boundaries':True,'restart_duplicates':0,'sources':'synthetic proposal fixture; complete mail-read evidence and forged-confirmation cases covered by Go tests'},indent=2))
        except Exception:
            log.flush();log.seek(0);print(log.read()[-8000:]);print(json.dumps({'errors':errors,'sent':sent},ensure_ascii=False)[-5000:]);raise
        finally:
            if process.poll() is None:os.killpg(process.pid,signal.SIGTERM);process.wait(timeout=15)
            server.shutdown();log.close()
if __name__=='__main__':main()
