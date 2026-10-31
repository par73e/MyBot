"""Private bridge to an explicitly attached original Friday instance."""
import os, re
from pathlib import Path
import httpx
from . import store as s

class NativeError(ValueError):
    def __init__(self,message,status=503):super().__init__(message);self.status=status

LABELS={'assistant_tasks':'待办与提醒','assistant_memory':'个人记忆','assistant_subscriptions':'到期管理','assistant_mail':'邮箱','assistant_weather':'天气','assistant_courses':'课程表','assistant_ai_news':'AI 新闻','assistant_github':'GitHub','assistant_weather_alerts':'天气预警','assistant_heartbeat':'作息与调度','assistant_morning':'晨报','assistant_evening':'晚间总结','assistant_proposals':'待办建议'}

async def request(uid,endpoint,body=None):
    source=s.native(uid)
    if not source:raise NativeError('当前工作区尚未关联助手',404)
    if source.startswith('instance:'):
        instance_id=source.removeprefix('instance:')
        if not re.fullmatch(r'[0-9a-f]{32}',instance_id):raise NativeError('助手实例标识无效',503)
        root=Path(os.environ.get('ASROBOT_INSTANCE_ROOT','/run/asrobot-instances'))
        socket=str(root/instance_id/'native.sock')
        token=root/instance_id/'native.token'
        source='friday_full'  # The private bridge accepts its fixed local alias.
    elif source=='friday_full':
        socket=os.environ.get('ASROBOT_NATIVE_SOCKET','/run/asrobot-native/native.sock')
        token=Path(os.environ.get('ASROBOT_NATIVE_TOKEN','/run/secrets/native.token'))
    else:raise NativeError('助手实例配置无效',503)
    try:
        async with httpx.AsyncClient(transport=httpx.AsyncHTTPTransport(uds=socket),timeout=190 if endpoint=='chat' else 55,trust_env=False) as client:
            r=await client.request('POST' if body is not None else 'GET','http://native/v1/'+endpoint,params={'source':source},headers={'Authorization':'Bearer '+token.read_text().strip()},json=body)
        if r.status_code!=200:
            detail=r.json().get('error','助手操作失败')
            raise NativeError(detail,r.status_code if r.status_code in (400,409,502,503) else 503)
        return r.json()
    except (OSError,httpx.HTTPError):raise NativeError('助手暂时无法连接，配置与数据仍保留') from None

async def chat(uid,text,source='web',authorize=None):
    from . import runtime as rt
    async with rt.turn_lock(uid):
        if authorize:authorize()
        if not s.active(uid):raise ValueError('账号不可用')
        s.message(uid,'user',text,source)
        result=await request(uid,'chat',{'text':text})
        if s.active(uid):s.message(uid,'assistant',result['text'],source)
        return result['text']
