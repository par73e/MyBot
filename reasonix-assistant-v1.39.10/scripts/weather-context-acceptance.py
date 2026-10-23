#!/usr/bin/env python3
"""Context-aware real bot/child/weather/SQLite/Weixin acceptance with a simulated model.
Requires test-weather-transport build overlay; never uses real accounts/secrets.
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
    binary=str(Path(args.binary).resolve())
    if 'test-weather-transport' not in subprocess.check_output([binary,'--version'],text=True):raise RuntimeError('requires fixture binary')
    with tempfile.TemporaryDirectory(prefix='mybot-weather-acceptance-') as tmp:
        root=Path(tmp);home=root/'home';work=root/'work';data=root/'fixtures'
        for p in [home,work,data,home/'assistant']:p.mkdir(exist_ok=True)
        zone=dt.timezone(dt.timedelta(hours=8));today=dt.datetime.now(zone).date()
        stamp=lambda hour,day=0:dt.datetime.combine(today+dt.timedelta(days=day),dt.time(hour),zone).isoformat()
        fixtures={'current':{'temperature':{'value':20,'unit':'C'},'condition':{'text':'阴'}},
          'daily':{'days':[{'forecastStartTime':stamp(0,n),'forecastEndTime':stamp(0,n+1),'temperatureMax':{'value':22-n,'unit':'C'},'temperatureMin':{'value':15-n,'unit':'C'}} for n in range(3)]},
          'hourly':{'hours':[{'forecastTime':stamp(h),'temperature':{'value':20,'unit':'C'},'precipitation':{'probability':.8,'type':'rain'}} for h in range(24)] + [{'forecastTime':stamp(h,1),'temperature':{'value':20,'unit':'C'},'precipitation':{'probability':.8}} for h in range(24)]},
          'air_current':{'indexes':[{'code':'cn-mee','aqi':42,'category':'优'}]},
          'air_hourly':{'hours':[{'forecastTime':stamp(h),'indexes':[{'code':'cn-mee','aqi':45,'category':'优'}]} for h in range(24)]},
          'alerts':{'alerts':[]},'geo':{'code':'200','location':[{'id':'fixture-city'}]},
          'history':{'code':'200','weatherDaily':{'tempMax':'24','tempMin':'17'}},
          'minutely':{'code':'200','minutely':[]},'indices':{'code':'200','daily':[{'date':str(today),'type':'1','text':'舒适'}]}}
        for key,value in fixtures.items():(data/(key+'.json')).write_text(json.dumps(value))
        wake={day:'23:59' for day in ['monday','tuesday','wednesday','thursday','friday','saturday','sunday']}
        (home/'assistant/weather.json').write_text(json.dumps({'enabled':True,'credential_file':str(root/'credential.json'),'locations_file':str(root/'locations.json'),'morning_enabled':True,'wake_times':wake}))
        (root/'locations.json').write_text(json.dumps({'timezone':'Asia/Shanghai','default_by_weekday':{day:('home' if day in ['saturday','sunday'] else 'school') for day in wake},'places':{'home':{'name':'Fixture home','latitude':31,'longitude':121},'school':{'name':'Fixture school','latitude':30,'longitude':120}}}))
        incoming=queue.Queue();sent=[];requests=[];errors=[];reads=[];selections=[]
        memories=home/'assistant/memory';memories.mkdir()
        def memory(key,body,start,end,archived=False):
            meta={'id':key,'revision':1,'subject':'验收行程','body':'','kind':'short','starts_at':start,'expires_at':end,'archived':archived,'updated_at':dt.datetime.now(zone).isoformat()}
            p=memories/(key+'.md');next_file=p.with_suffix('.next');next_file.write_text('---\n'+json.dumps(meta)+'\n---\n\n'+body+'\n');next_file.replace(p)
        future_id='111111111111111111111111';trip_id='222222222222222222222222';month_id='333333333333333333333333'
        future_body='FUTURE_HANGZHOU：Sir从'+str(today+dt.timedelta(days=1))+'开始去浙江杭州旅行三天，到'+str(today+dt.timedelta(days=4))+'零点结束。'
        memory(future_id,future_body,stamp(0,1),stamp(0,4))
        month_start=dt.datetime.now(zone)+dt.timedelta(days=30)
        memory(month_id,'MONTH_TRIP_KNOWN：一个月后去北京参加会议，持续两天。',month_start.isoformat(),(month_start+dt.timedelta(days=2)).isoformat())
        def state_from(messages):
            snapshots=[]
            for m in messages:
                text=m.get('content','')
                if not isinstance(text,str):continue
                marker='<personal_assistant_current_state>'
                if marker not in text:continue
                block=text.split(marker)[-1].split('</personal_assistant_current_state>')[0]
                start=block.find('\n{')
                if start>=0:
                    try:snapshots.append(json.JSONDecoder().raw_decode(block[start+1:])[0])
                    except ValueError:pass
            if not snapshots:raise RuntimeError('current personal state missing from model context')
            return max(snapshots,key=lambda v:dt.datetime.fromisoformat(v['now']))

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
                if self.path.endswith('/sendmessage'):sent.append(body);return self.reply({'ret':0,'errcode':0,'message_id':str(len(sent))})
                if '/ilink/' in self.path:return self.reply({'ret':0,'errcode':0,'typing_ticket':'fixture'})
                if not self.path.endswith('/chat/completions'):return self.reply({'error':'unexpected path'})
                requests.append(body);messages=body.get('messages',[]);rendered=json.dumps(messages,ensure_ascii=False)
                bg='[Host notification contract]' in rendered
                names={t['function']['name'] for t in body.get('tools',[])}
                if 'assistant_weather' not in names:errors.append('weather tool missing')
                if bg and names-{'assistant_weather','assistant_tasks','assistant_memory','assistant_history'}:errors.append('unsafe morning tools')
                state=state_from(messages)
                if 'MONTH_TRIP_KNOWN' not in json.dumps(state.get('known_future_memories',[])):errors.append('month-away memory hidden')
                last_user=max((i for i,m in enumerate(messages) if m.get('role')=='user'),default=0)
                question=str(messages[last_user].get('content',''))
                relevant=messages if bg else messages[last_user+1:]
                results=[m for m in relevant if m.get('role')=='tool']
                mode='briefing' if bg else 'future' if 'FUTURE_FIXTURE' in question else 'school' if 'SCHOOL_FIXTURE' in question else 'known' if 'MONTH_FIXTURE' in question else 'aqi' if 'AQI_FIXTURE' in question else 'evening'
                active_trip='ACTIVE_HANGZHOU' in json.dumps(state.get('active_memories',[]))
                trip=mode=='future' or active_trip and mode in ['briefing','aqi']
                if mode=='future' and 'FUTURE_HANGZHOU' not in json.dumps(state.get('known_future_memories',[])):errors.append('tomorrow travel not visible today')
                reference=state.get('weather_defaults',{})
                candidate=reference.get('candidate',{}).get('place_id')
                if candidate not in ['home','school']:errors.append('default candidate missing before model decision')
                if mode=='known':
                    final='MONTH_RESULT_FIXTURE：Sir，一个月后您计划去北京参加两天会议。';delta={'content':final};finish='stop'
                elif not results:
                    query={'action':'query','view':'briefing'} if bg else {'action':'query','date':str(today+dt.timedelta(days=1)),'components':['daily','hourly']} if mode=='future' else {'action':'query','components':['air_current']} if mode=='aqi' else {'action':'query','components':['current']} if mode=='school' else {'action':'query','start':stamp(18),'end':stamp(23),'components':['hourly','alerts']}
                    if trip:query.update(latitude=30.27,longitude=120.15,label='杭州',location_source='memory',location_reason='根据查询日期对应的旅行记忆')
                    else:query.update(place='school' if mode=='school' else candidate,location_source='explicit_question' if mode=='school' else 'schedule',location_reason='用户明确问学校' if mode=='school' else '记忆没有覆盖所查时段，采用默认候选')
                    delta={'tool_calls':[{'index':0,'id':f'call-{len(requests)}','type':'function','function':{'name':'assistant_weather','arguments':json.dumps(query)}}]};finish='tool_calls'
                else:
                    result=json.loads(results[-1]['content']);parts=result.get('components',{});place=result['place']
                    expected='杭州' if trip else 'Fixture school' if mode=='school' or candidate=='school' else 'Fixture home'
                    if place['name']!=expected:errors.append('Agent-chosen destination overwritten: '+place['name']+' expected '+expected)
                    selections.append({'mode':mode,'place':place['name']})
                    if mode=='briefing':
                        if set(parts)!=set(['current','daily','hourly','air_current','air_hourly','alerts','minutely','indices','history']):errors.append('briefing components missing')
                        final=json.dumps({'notify':True,'message':'Sir，MORNING_FIXTURE：按您的杭州旅行安排查询，今天15到22度，晚间可能下雨。','reason':'daily briefing'})
                    else:final=mode.upper()+'_RESULT_FIXTURE：Sir，已按'+place['name']+'查询天气。'
                    reads.append(mode);delta={'content':final};finish='stop'
                raw=('data: '+json.dumps({'choices':[{'delta':delta,'finish_reason':finish}]})+'\n\ndata: [DONE]\n\n').encode()
                self.send_response(200);self.send_header('Content-Type','text/event-stream');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
        server=ThreadingHTTPServer(('127.0.0.1',0),Handler);threading.Thread(target=server.serve_forever,daemon=True).start();base='http://127.0.0.1:'+str(server.server_port)
        (home/'config.toml').write_text(f'''
default_model = "fixture/model"
[agent]
temperature = 0.0
[permissions]
mode = "ask"
allow = ["assistant_weather", "assistant_tasks", "assistant_memory"]
[[providers]]
name = "fixture"
kind = "openai"
base_url = "{base}/v1"
models = ["model"]
default = "model"
api_key_env = "WEATHER_FIXTURE_KEY"
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
token_env = "WEATHER_FIXTURE_WEIXIN"
api_base = "{base}"
''')
        env=dict(os.environ,REASONIX_HOME=str(home),HOME=str(home),REASONIX_ASSISTANT='1',TZ='Asia/Shanghai',WEATHER_FIXTURE_KEY='fixture',WEATHER_FIXTURE_WEIXIN='fixture',WEATHER_TEST_DATA_DIR=str(data),REASONIX_TELEMETRY='off')
        command=[binary,'bot','start','--channels','weixin','--dir',str(work),'--heartbeat','--heartbeat-poll','1s']
        log=open(root/'bot.log','w+');process=None
        def dbrows(kind):
            p=home/'assistant/state.sqlite3'
            if not p.exists():return []
            try:
                with sqlite3.connect(p) as db:return [json.loads(r[0]) for r in db.execute('SELECT payload FROM assistant_entities WHERE kind=?',(kind,))]
            except sqlite3.OperationalError:return []
        def wait_for(predicate,seconds=25):
            deadline=time.monotonic()+seconds
            while time.monotonic()<deadline:
                if predicate():return
                if process.poll() is not None:raise RuntimeError('bot exited')
                time.sleep(.2)
            raise RuntimeError('acceptance timed out')
        def ask(number,text):
            time.sleep(.6)
            incoming.put({'message_id':'in-'+str(number),'from_user_id':'fixture-owner','to_user_id':'fixture','msg_type':1,'context_token':'fixture-context','item_list':[{'type':1,'text_item':{'text':text}}]})
        try:
            process=subprocess.Popen(command,cwd=work,env=env,stdout=log,stderr=log,start_new_session=True)
            wait_for(lambda:any(j.get('id')=='weather-morning-check' for j in dbrows('job')))
            time.sleep(2)
            if requests:errors.append('outside wake window invoked model')
            ask(1,'FUTURE_FIXTURE 明天那里天气怎么样？');wait_for(lambda:'FUTURE_RESULT_FIXTURE' in json.dumps(sent))
            ask(2,'EVENING_FIXTURE 今天晚上会不会下雨？');wait_for(lambda:'EVENING_RESULT_FIXTURE' in json.dumps(sent))
            ask(3,'MONTH_FIXTURE 我一个月后有什么安排？');wait_for(lambda:'MONTH_RESULT_FIXTURE' in json.dumps(sent))
            memory(future_id,future_body,stamp(0,1),stamp(0,4),archived=True)
            memory(trip_id,'ACTIVE_HANGZHOU：行程变更，Sir从今天开始已在浙江杭州旅行，到三天后零点结束。',stamp(0),stamp(0,3))
            ask(4,'AQI_FIXTURE 现在外面空气质量怎么样？');wait_for(lambda:'AQI_RESULT_FIXTURE：Sir，已按杭州' in json.dumps(sent,ensure_ascii=False))
            ask(5,'SCHOOL_FIXTURE 今天学校天气怎么样？');wait_for(lambda:'SCHOOL_RESULT_FIXTURE' in json.dumps(sent))
            now=dt.datetime.now(zone)
            job={'id':'weather-morning-fixture','title':'Weather fixture morning','prompt':'为Sir生成全天晨报。先读默认候选，再综合当前和未来记忆决定地点，然后调用assistant_weather，action=query，view=briefing并传入所选地点，必须返回notify=true的通知。','at':(now-dt.timedelta(seconds=1)).isoformat(),'endsAt':(now+dt.timedelta(minutes=5)).isoformat(),'timezone':'Asia/Shanghai','kind':'agent','capability':'weather','enabled':True,'approvalMode':'read-only','decide_notification':True,'newConversationEachRun':True,'workspaceRoot':str(work),'model':'fixture/model','target':{'connection_id':'weixin','domain':'weixin','chat_id':'fixture-owner','chat_type':'dm'},'createdAt':int(time.time()*1000)}
            with sqlite3.connect(home/'assistant/state.sqlite3',timeout=5) as db:db.execute('INSERT INTO assistant_entities(kind,id,payload) VALUES(?,?,?)',('job',job['id'],json.dumps(job)))
            wait_for(lambda:'MORNING_FIXTURE' in json.dumps(sent))
            if not dbrows('weather_read_receipt'):errors.append('missing execution-bound read receipt')
            wait_for(lambda:next(j for j in dbrows('job') if j['id']==job['id']).get('history',[{}])[-1].get('status')=='delivered')
            os.killpg(process.pid,signal.SIGTERM);process.wait(timeout=15)
            before=(len(requests),len(sent));process=subprocess.Popen(command,cwd=work,env=env,stdout=log,stderr=log,start_new_session=True);time.sleep(4)
            if before!=(len(requests),len(sent)):errors.append('restart duplicated analysis/delivery')
            memory(trip_id,'ACTIVE_HANGZHOU：已结束的杭州旅行。',stamp(0,-4),stamp(0,-1))
            ask(6,'AQI_FIXTURE 现在外面空气质量怎么样？');wait_for(lambda:('AQI_RESULT_FIXTURE：Sir，已按'+('Fixture home' if today.weekday()>=5 else 'Fixture school')) in json.dumps(sent,ensure_ascii=False))
            if reads!=['future','evening','aqi','school','briefing','aqi']:errors.append('unexpected actual query sequence '+str(reads))
            report={'passed':not errors,'real_bot_child_tool_sqlite':True,'simulated_weather_model_weixin':True,'test_messages_to_real_weixin':0,'queries':reads,'location_selections':selections,'month_away_memory_visible':True,'restart_dedup':True,'model_requests':len(requests),'outbound_fixture_messages':len(sent),'errors':errors}
            print(json.dumps(report,ensure_ascii=False,indent=2))
            if errors:raise RuntimeError('acceptance failed')
        except Exception:
            log.flush();log.seek(0);print(log.read()[-14000:]);raise
        finally:
            if process and process.poll() is None:os.killpg(process.pid,signal.SIGTERM);process.wait(timeout=15)
            server.shutdown();log.close()

if __name__=='__main__':main()
