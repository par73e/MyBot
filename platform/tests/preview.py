"""Disposable browser acceptance host; fake model/WeChat, never production.

ASROBOT_DATA=/tmp/... ASROBOT_ORIGIN=http://localhost:18981 python -m tests.preview
Start with `python tests/preview.py` from platform, PYTHONPATH=.
"""
import asyncio, os, secrets
from pathlib import Path
if not os.environ.get('ASROBOT_DATA','').startswith(('/tmp/','/private/tmp/')): raise SystemExit('Preview requires disposable /tmp data')
from app.main import app, ph
from app import store as s, runtime as rt
s.init()
with s.db() as c:
    for login,role in [('qa_admin','admin'),('qa_user','user')]:
        if not c.execute('SELECT 1 FROM users WHERE login=?',(login,)).fetchone(): c.execute('INSERT INTO users(id,login,password,role,created) VALUES(?,?,?,?,?)',(s.ident(),login,ph.hash('qa-only'),role,s.now()))

attempts={}; inbound_sent=set()
async def fake_model(uid,messages,tools=False):
    cfg=s.settings(uid)
    if not cfg['provider'] or not cfg['model'] or not s.secret(uid,'model'): raise ValueError('请先在模型设置中填写服务商、模型和密钥')
    return {'role':'assistant','content':'模拟验收回复：已读取当前账号的助手配置。'}

async def fake_wechat(base,path,token=None,body=None,params=None):
    if path.endswith('get_bot_qrcode'):
        code='qa-'+secrets.token_hex(8); attempts[code]=0
        return {'qrcode':code,'qrcode_img_content':'模拟验收二维码，不用于真实微信连接：'+code}
    if path.endswith('get_qrcode_status'):
        code=params['qrcode']; attempts[code]+=1
        if params.get('verify_code')=='123456': return {'status':'confirmed','ilink_bot_id':'qa-bot','ilink_user_id':'qa-owner','bot_token':'qa-credential','baseurl':rt.WX}
        return {'status':'wait' if attempts[code]==1 else 'scaned' if attempts[code]==2 else 'need_verifycode'}
    if path.endswith('getupdates'):
        await asyncio.sleep(1)
        if token not in inbound_sent:
            inbound_sent.add(token)
            return {'get_updates_buf':'mock-cursor','msgs':[{'from_user_id':'qa-owner','message_type':1,'message_id':'qa-first','context_token':'qa-context','item_list':[{'type':1,'text_item':{'text':'模拟微信入站验收'}}]}]}
        return {'get_updates_buf':'mock-cursor','msgs':[]}
    if path.endswith('sendmessage'): return {'ret':0,'message_id':'qa-outbound'}
    raise ValueError('模拟环境不支持此操作')
rt.model=fake_model;rt.wx_request=fake_wechat
if __name__=='__main__':
    import uvicorn
    uvicorn.run(app,host='127.0.0.1',port=18981,access_log=False)
