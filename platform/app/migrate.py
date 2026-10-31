"""Private CLI for reviewed Friday configuration imports, never a public endpoint.

Input is a JSON line on stdin. Credentials never appear in arguments or output.
Only the named destination account is changed; source files remain read-only.
"""
import sys, json, hashlib, re, os
from . import store as s, runtime as rt
from .main import ph, password_ok

def persona(text):
    parts=re.split(r'^## ',text,flags=re.M)
    kept=[parts[0].strip()]
    for part in parts[1:]:
        title=part.splitlines()[0].strip()
        if title in ('表达与排版判断','可靠性与边界'): kept.append('## '+part.strip())
    kept.append('当前运行于私人助手平台，只使用当前已提供的业务工具。天气、邮箱、课程、晨报、新闻等尚未接入时如实说明。不要套用旧工具名或承诺终端、文件和插件能力。')
    result='\n\n'.join(kept)
    if len(result)>6000: raise ValueError('人格配置超过目标长度，需重新预览，禁止截断')
    return result

def prepare(payload):
    source=payload['source']; model=source['model']
    if model['base_url']!=rt.PROVIDERS['qianwen_maas'][1] or model['model']!='qwen3.8-flash' or model['effort'] not in ('low','medium','xhigh'): raise ValueError('模型配置与已审核映射不符')
    weekly=source['weekly_schedule']; overrides=source['date_overrides']; rt.validate_schedules(weekly,overrides)
    cfg=json.loads(json.dumps(s.DEFAULT))
    name=source.get('name','我的助手'); address=source.get('address','')
    if not isinstance(name,str) or not name.strip() or len(name)>50 or not isinstance(address,str) or len(address)>50: raise ValueError('助手名称或称呼无效')
    cfg.update(name=name.strip(),address=address.strip(),style='自然、可靠、简洁；先给结论，按内容决定排版，不机械套模板',prompt=persona(source['prompt']),provider='qianwen_maas',model=model['model'],reasoning_effort=model['effort'],weekly_schedule=weekly,date_overrides=overrides)
    pending=sorted(source.get('enabled_modules',[]))
    cfg['migration_info']={'source':'legacy-assistant','scope':'身份、表达偏好、模型与作息','pending_modules':pending,'wechat':'原Friday继续运行，未接管连接','data':'未导入记忆、待办和历史'}
    manifest={'version':1,'source':'friday-single-owner','scope':'configuration-only','settings_fingerprint':s.digest(json.dumps(cfg,ensure_ascii=False,sort_keys=True)),'source_fingerprint':source['fingerprint'],'pending_modules':pending}
    mid=s.digest(json.dumps(manifest,sort_keys=True))
    return cfg,manifest,mid

def apply(payload,commit=False):
    login=payload.get('login')
    expected = os.environ.get('ASROBOT_MIGRATION_LOGIN')
    if not expected or login != expected: raise ValueError('迁移目标与运维人员指定的账号不符')
    cfg,manifest,mid=prepare(payload)
    with s.db() as c:
        existing=c.execute('SELECT * FROM users WHERE login=?',(login,)).fetchone()
        done=c.execute('SELECT user_id FROM migrations WHERE id=?',(mid,)).fetchone()
    if done:
        if not existing or existing['id']!=done[0]: raise ValueError('迁移归属异常')
        return {'status':'already-applied','login':login,'migration_id':mid}
    if existing: raise ValueError('目标账号已存在但不属于本批迁移，需显式处理冲突，禁止覆盖')
    password_ok(payload.get('password'))
    key=payload.get('model_key')
    if not isinstance(key,str) or not key or len(key)>1024: raise ValueError('源模型凭据不可用')
    preview={'login':login,'name':cfg['name'],'address':cfg['address'],'provider':cfg['provider'],'model':cfg['model'],'reasoning_effort':cfg['reasoning_effort'],'weekly_schedule':cfg['weekly_schedule'],'date_overrides':cfg['date_overrides'],'pending_modules':manifest['pending_modules'],'migration_id':mid,'key_available':True}
    if not commit: return {'status':'preview',**preview}
    encoded=ph.hash(payload['password']); uid=s.ident()
    with s.db() as c:
        c.execute('BEGIN IMMEDIATE')
        if c.execute('SELECT 1 FROM users WHERE login=?',(login,)).fetchone(): raise ValueError('目标账号刚发生变化，已取消迁移')
        c.execute('INSERT INTO users(id,login,password,role,must_change,created) VALUES(?,?,?,?,0,?)',(uid,login,encoded,'admin',s.now()))
        c.execute('INSERT INTO settings VALUES(?,?,1)',(uid,json.dumps(cfg,ensure_ascii=False)))
        c.execute('INSERT INTO secrets VALUES(?,?,?)',(uid,'model',s.encrypt(key)))
        c.execute('INSERT INTO migrations VALUES(?,?,?,?)',(mid,uid,json.dumps(manifest),s.now()))
        c.execute('INSERT INTO activity(user_id,event,status,created) VALUES(?,?,?,?)',(uid,'导入Friday基础配置','success',s.now()))
    return {'status':'applied',**preview,'password_storage':'argon2id','role':'admin','private_content_imported':False,'wechat_changed':False}

def main():
    s.init(); payload=json.loads(sys.stdin.readline())
    try: print(json.dumps(apply(payload,commit=len(sys.argv)>1 and sys.argv[1]=='commit'),ensure_ascii=False))
    except (ValueError,KeyError,TypeError):
        print(json.dumps({'status':'failed','message':'迁移校验或冲突检查失败，未自动覆盖已有账号'},ensure_ascii=False)); raise SystemExit(1)
if __name__=='__main__': main()
