"""Owner-bound reference history. Only the server can attach an archive source."""
import os
from pathlib import Path
import httpx
from . import store as s

class ArchiveUnavailable(ValueError): pass

async def archive(uid, endpoint, **params):
    source=s.workspace(uid)['legacy_source']
    if not source: raise ValueError('当前工作区未关联旧会话')
    socket=os.environ.get('ASROBOT_HISTORY_SOCKET','/run/asrobot-history/history.sock')
    token=Path(os.environ.get('ASROBOT_HISTORY_TOKEN','/run/secrets/history.token'))
    try:
        transport=httpx.AsyncHTTPTransport(uds=socket)
        async with httpx.AsyncClient(transport=transport,timeout=45,trust_env=False) as client:
            r=await client.get('http://history/v1/'+endpoint,params={'source':source,**params},headers={'Authorization':'Bearer '+token.read_text().strip()})
        if r.status_code==400: raise ValueError('旧会话查询参数无效，请重新选择日期')
        if r.status_code!=200: raise ArchiveUnavailable('旧会话暂时无法读取，请稍后重试；原记录仍保留')
        return r.json()
    except (OSError,httpx.HTTPError):
        raise ArchiveUnavailable('旧会话读取服务暂时不可用；原记录仍保留') from None
