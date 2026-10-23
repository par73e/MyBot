#!/usr/bin/env python3
"""Isolated real-cloud-model acceptance; no production memories or Weixin sends."""
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import pwd
import shutil
import signal
import sqlite3
import subprocess
import sys
import time

ROOT=Path('/var/lib/mybot/weather/qweather');HOME=ROOT/'context-acceptance-home';PROD=Path('/var/lib/mybot/reasonix');BINARY=Path('/tmp/mybot-weather-context-candidate')
SHA=sys.argv[1];user=pwd.getpwnam('mybot');zone=dt.timezone(dt.timedelta(hours=8));now=dt.datetime.now(zone);today=now.date()
if os.geteuid()!=0 or hashlib.sha256(BINARY.read_bytes()).hexdigest()!=SHA:raise RuntimeError('root/checked candidate required')
for folder in [HOME,HOME/'assistant',HOME/'assistant/memory',HOME/'prompts',HOME/'workspace']:
    folder.mkdir(mode=0o700,exist_ok=True);os.chown(folder,user.pw_uid,user.pw_gid)
for source,target in [(PROD/'config.toml',HOME/'config.toml'),(PROD/'.env',HOME/'.env'),(PROD/'assistant/weather.json',HOME/'assistant/weather.json'),(PROD/'prompts/friday.md',HOME/'prompts/friday.md')]:
    shutil.copyfile(source,target);target.chmod(0o600);os.chown(target,user.pw_uid,user.pw_gid)

def memory(key,body,start,end,archived=False):
    meta={'id':key,'revision':1,'subject':'隔离验收行程','body':'','kind':'short','starts_at':start.isoformat(),'expires_at':end.isoformat(),'archived':archived,'updated_at':dt.datetime.now(zone).isoformat()}
    p=HOME/'assistant/memory'/(key+'.md');p.write_text('---\n'+json.dumps(meta)+'\n---\n\n'+body+'\n');p.chmod(0o600);os.chown(p,user.pw_uid,user.pw_gid)

def run_case(name,prompt,background=False):
    attempt=str(int(time.time()*1000))
    env=['HOME=/var/lib/mybot','REASONIX_HOME='+str(HOME),'REASONIX_ASSISTANT=1','REASONIX_TELEMETRY=0','TZ=Asia/Shanghai']
    if background:env+=['REASONIX_ASSISTANT_BACKGROUND=1','REASONIX_ASSISTANT_WEATHER_BACKGROUND=1','REASONIX_ASSISTANT_JOB_ID=weather-context-smoke','REASONIX_ASSISTANT_WEATHER_ATTEMPT='+attempt]
    command=['sudo','-n','-u','mybot','env',*env,str(BINARY),'run','--permission-mode','read-only','--output-format','stream-json','--dir',str(HOME/'workspace'),'--max-steps','8','--',prompt]
    p=subprocess.Popen(command,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
    try:out,err=p.communicate(timeout=100)
    except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGTERM);p.communicate(timeout=8);raise
    if p.returncode:raise RuntimeError(name+' model execution failed: '+err[-600:])
    events=[json.loads(line) for line in out.splitlines() if line.strip()]
    final=next(e for e in reversed(events) if e.get('type')=='result')
    if final.get('is_error'):raise RuntimeError(name+' model reported failure')
    result=[]
    for e in events:
        t=e.get('tool') or {}
        if e.get('kind')!='tool_result' or t.get('name')!='assistant_weather' or t.get('err'):continue
        try:v=json.loads(t.get('output',''))
        except ValueError:continue
        if isinstance(v,dict) and 'components' in v:result.append(v)
    p=ROOT/('context-model-'+name+'.jsonl');p.write_text(out);p.chmod(0o600);os.chown(p,user.pw_uid,user.pw_gid)
    if background:
        with sqlite3.connect(HOME/'assistant/state.sqlite3') as db:
            receipt=hashlib.sha256(('weather-context-smoke\0'+attempt).encode()).hexdigest()[:32]
            if db.execute("SELECT payload FROM assistant_entities WHERE kind='weather_read_receipt' AND id=?",(receipt,)).fetchone()!=('true',):raise RuntimeError('missing actual briefing receipt')
        answer=json.loads(final['result'])
        if not answer.get('notify') or not answer.get('message'):raise RuntimeError('morning failed to produce notification')
    return result,final['result']

month=now+dt.timedelta(days=30)
memory('444444444444444444444444','已确认未来计划：'+str(month.date())+'去北京参加会议，持续两天。',month,month+dt.timedelta(days=2))
records=[]
try:
    results,text=run_case('month','Sir一个月后有什么已记录的行程？只根据记忆回答，不修改任何记忆或任务，不发送其他消息。')
    if '北京' not in text or results:raise RuntimeError('month-away memory not understood now')
    records.append({'case':'month-away-memory','passed':True,'weather_queries':0})
    trip='555555555555555555555555';future='666666666666666666666666'
    midnight=dt.datetime.combine(today,dt.time(),zone)
    memory(trip,'行程确认：Sir从'+str(today)+'开始到'+str(today+dt.timedelta(days=3))+'零点，在浙江省杭州市旅行，日常活动地点为杭州。',midnight,midnight+dt.timedelta(days=3))
    morning=Path('/tmp/mybot-weather-context-morning-prompt.txt').read_text()+'\n宿主日期：'+str(today)+'（北京时间）；起床时间11:00。'+Path('/tmp/mybot-weather-context-decision-prompt.txt').read_text()
    results,text=run_case('morning-trip',morning,True)
    if not results:raise RuntimeError('no actual morning weather tool result')
    place=results[-1]['place']
    if not (29.8<float(place['latitude'])<30.7 and 119.7<float(place['longitude'])<120.8):raise RuntimeError('morning queried schedule city instead of trip city')
    if results[-1].get('location_is_schedule_default'):raise RuntimeError('morning silently used schedule fallback')
    records.append({'case':'morning-trip','passed':True,'selected_city':'杭州','actual_weather_result':True,'location_reason':results[-1].get('location_reason','')})
    results,text=run_case('explicit-school','现在我想知道学校的空气质量怎么样？请实际查询，只回答学校的当前空气质量，不修改记忆、任务或发送其他消息。')
    if not results or '校' not in results[-1]['place']['name']:raise RuntimeError('explicit school lost to trip memory')
    records.append({'case':'explicit-school-during-trip','passed':True,'selected_configured_place':'school','actual_weather_result':True})
    memory(trip,'已结束的杭州旅行。',midnight-dt.timedelta(days=4),midnight-dt.timedelta(days=1))
    results,text=run_case('expired-trip','现在外面空气质量怎么样？请实际查询，不修改记忆、任务或发送其他消息。')
    status=json.loads(subprocess.check_output(['sudo','-n','-u','mybot','env','REASONIX_HOME='+str(HOME),str(BINARY),'assistant','weather'],text=True,timeout=10))
    candidate=status['candidate']['name']
    if not results or results[-1]['place']['name']!=candidate:raise RuntimeError('expired trip did not restore schedule city')
    records.append({'case':'expired-trip-restores-default','passed':True,'selected_default':True,'actual_weather_result':True})
    memory(future,'未来已确认行程：从'+str(today+dt.timedelta(days=1))+'开始去浙江省杭州市旅行三天，到'+str(today+dt.timedelta(days=4))+'零点结束。',midnight+dt.timedelta(days=1),midnight+dt.timedelta(days=4))
    results,text=run_case('tomorrow-trip','帮我看看明天那边的天气大致怎么样，概括气温和下雨情况。请实际查询，不修改记忆、任务或发送其他消息。')
    place=results[-1]['place'] if results else {}
    if not (29.8<float(place.get('latitude',0))<30.7 and 119.7<float(place.get('longitude',0))<120.8) or results[-1]['date']!=str(today+dt.timedelta(days=1)):raise RuntimeError('future memory not used for tomorrow query')
    records.append({'case':'future-trip-for-tomorrow','passed':True,'selected_city':'杭州','target_date':results[-1]['date'],'actual_weather_result':True})
    print(json.dumps({'passed':True,'version':'v1.39.10-assistant.8-weather-context','binary_sha256':SHA,'real_cloud_model':True,'real_weather_api':True,'isolated_memories_only':True,'test_weixin_messages':0,'checks':records},ensure_ascii=False,indent=2))
finally:
    for name in ['.env','config.toml']:
        p=HOME/name
        if p.is_file() and not p.is_symlink():p.unlink()
