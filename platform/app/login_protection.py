"""Login challenges and failure limits; SQLite is the durable fallback and replay guard."""
import base64, io, logging, os, secrets
from PIL import Image, ImageDraw, ImageFont
import redis
from . import store as s

log = logging.getLogger(__name__)
CAPTCHA_TTL = 300
FAIL_TTL = 900
FAIL_LIMIT = 5

def client():
    url = os.environ.get('ASROBOT_REDIS_URL', '')
    if not url:
        return None
    return redis.Redis.from_url(url, socket_connect_timeout=0.2, socket_timeout=0.2, decode_responses=True)

def redis_call(method, *args, **kwargs):
    r = client()
    if r is None:
        return None
    try:
        return getattr(r, method)(*args, **kwargs)
    except redis.RedisError:
        log.warning('Redis login protection unavailable; using SQLite fallback')
        return None
    finally:
        r.close()

def create_captcha():
    key = secrets.token_urlsafe(24)
    answer = ''.join(secrets.choice('23456789') for _ in range(4))
    expected = s.digest(key + ':' + answer)
    with s.db() as c:
        c.execute('DELETE FROM login_captchas WHERE expires<=?', (s.now(),))
        c.execute('INSERT INTO login_captchas VALUES(?,?,?)', (s.digest(key), expected, s.now()+CAPTCHA_TTL))
    redis_call('set', 'asrobot:captcha:'+s.digest(key), expected, ex=CAPTCHA_TTL)
    image = Image.new('RGB',(140,50),'white'); draw = ImageDraw.Draw(image)
    # A built-in font is always present in the container; scale it for readability.
    layer=Image.new('RGB',(70,25),'white'); ImageDraw.Draw(layer).text((8,5), '  '.join(answer), fill=(20,45,65),font=ImageFont.load_default())
    image.paste(layer.resize((140,50)))
    for _ in range(5):
        draw.line((secrets.randbelow(140),secrets.randbelow(50),secrets.randbelow(140),secrets.randbelow(50)), fill=(190,200,210),width=1)
    buf=io.BytesIO(); image.save(buf,format='PNG')
    return {'key':key,'image':'data:image/png;base64,'+base64.b64encode(buf.getvalue()).decode(),'expires_in':CAPTCHA_TTL}

def consume_captcha(key, answer):
    if not isinstance(key,str) or not isinstance(answer,str) or len(key)>100 or len(answer)>10:
        return False
    ident=s.digest(key)
    # SQLite is the atomic replay guard even if Redis becomes unavailable or recovers.
    with s.db() as c:
        c.execute('BEGIN IMMEDIATE')
        row=c.execute('SELECT answer,expires FROM login_captchas WHERE id=?',(ident,)).fetchone()
        c.execute('DELETE FROM login_captchas WHERE id=?',(ident,))
    cached=redis_call('getdel','asrobot:captcha:'+ident)
    candidate=s.digest(key+':'+answer.strip())
    return bool(row and row['expires']>s.now() and secrets.compare_digest(row['answer'],candidate) and (cached is None or secrets.compare_digest(cached,candidate)))

def locked(identity):
    with s.db() as c:
        row=c.execute('SELECT count,expires FROM login_failures WHERE id=?',(identity,)).fetchone()
    cached=redis_call('get','asrobot:login-fail:'+identity)
    cached_count=int(cached) if cached and cached.isdigit() else 0
    return bool((row and row['expires']>s.now() and row['count']>=FAIL_LIMIT) or cached_count>=FAIL_LIMIT)

def failed(identity):
    with s.db() as c:
        c.execute('BEGIN IMMEDIATE')
        c.execute('DELETE FROM login_failures WHERE expires<=?',(s.now(),))
        c.execute('INSERT INTO login_failures VALUES(?,1,?) ON CONFLICT(id) DO UPDATE SET count=count+1',(identity,s.now()+FAIL_TTL))
    # Redis is shared, bounded auxiliary state; the SQLite copy survives outages.
    script="local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],ARGV[1]); end; return n"
    redis_call('eval',script,1,'asrobot:login-fail:'+identity,FAIL_TTL)

def clear(identity):
    with s.db() as c:
        c.execute('DELETE FROM login_failures WHERE id=?',(identity,))
    redis_call('delete','asrobot:login-fail:'+identity)
