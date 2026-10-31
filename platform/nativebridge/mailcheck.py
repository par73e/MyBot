"""Validate existing owner credentials; no mail content, flags, or send calls."""
import concurrent.futures,json,time,os
from pathlib import Path
ns={'__name__':'asrobot_mail_verification'}
exec(compile(Path(__file__).with_name('mail_transport.py').read_bytes(),'mail_transport','exec'),ns)
home=os.environ.get('REASONIX_HOME')
if not home or not Path(home).is_absolute():
    raise SystemExit('an explicit instance home is required')
mail_config=Path(home)/'assistant/mail.json'
cfg=json.loads(mail_config.read_text()) if mail_config.exists() else {'accounts': []}
def check(a):
    client=None
    try:
        client=ns['Gmail' if a['provider']=='gmail' else 'QQ'](a)
        if a['provider']=='gmail':
            p=client.get('profile');ok=p.get('emailAddress','').lower()==a['address'].lower()
        else:ok=client.c.noop()[0]=='OK'
        return {'id':a['id'],'provider':a['provider'],'authenticated':ok,'checked_at':int(time.time())}
    except Exception as e:
        return {'id':a['id'],'provider':a['provider'],'authenticated':False,'error_type':type(e).__name__,'http_status':getattr(e,'code',None),'checked_at':int(time.time())}
    finally:
        if client:
            try:client.close()
            except Exception:pass
with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
    print(json.dumps(list(pool.map(check,cfg['accounts']))))
