"""Tenant-scoped storage. All user data queries require a server-selected owner."""
import os, json, sqlite3, secrets, time, hashlib
from pathlib import Path
from contextlib import contextmanager
from cryptography.fernet import Fernet

DATA = Path(os.environ.get('ASROBOT_DATA', '/data'))
def key_file(): return Path(os.environ.get('ASROBOT_KEY_FILE',str(DATA/'master.key')))
def ident(): return secrets.token_hex(16)
def digest(s): return hashlib.sha256(s.encode()).hexdigest()
def now(): return int(time.time())

@contextmanager
def db():
    c = sqlite3.connect(str(DATA/'platform.sqlite3'), timeout=10)
    c.row_factory = sqlite3.Row
    c.execute('PRAGMA foreign_keys=ON')
    try:
        yield c
        c.commit()
    except BaseException:
        c.rollback(); raise
    finally: c.close()

def init():
    DATA.mkdir(parents=True, exist_ok=True); DATA.chmod(0o700)
    key = key_file()
    if not key.exists():
        fd = os.open(str(key), os.O_CREAT|os.O_EXCL|os.O_WRONLY, 0o600)
        with os.fdopen(fd,'wb') as f: f.write(Fernet.generate_key())
    with db() as c:
        c.execute('PRAGMA journal_mode=WAL')
        c.executescript('''
CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,login TEXT UNIQUE NOT NULL,password TEXT NOT NULL,role TEXT NOT NULL DEFAULT 'user',status TEXT NOT NULL DEFAULT 'active',must_change INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,user_id TEXT REFERENCES users(id) ON DELETE CASCADE,csrf TEXT NOT NULL,expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS invites(hash TEXT PRIMARY KEY,role TEXT NOT NULL,expires INTEGER NOT NULL,used INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS settings(user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,body TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS secrets(user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,kind TEXT NOT NULL,value TEXT NOT NULL,PRIMARY KEY(user_id,kind));
CREATE TABLE IF NOT EXISTS entities(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,kind TEXT NOT NULL,body TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 1,created INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS entities_owner ON entities(user_id,kind);
CREATE TABLE IF NOT EXISTS messages(id INTEGER PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,role TEXT NOT NULL,text TEXT NOT NULL,source TEXT NOT NULL,created INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS messages_owner ON messages(user_id,created);
CREATE TABLE IF NOT EXISTS activity(id INTEGER PRIMARY KEY,user_id TEXT REFERENCES users(id) ON DELETE CASCADE,event TEXT NOT NULL,status TEXT NOT NULL,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS bindings(user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,external_id TEXT UNIQUE NOT NULL,status TEXT NOT NULL DEFAULT 'bound',last_poll INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS pairings(id TEXT PRIMARY KEY,user_id TEXT REFERENCES users(id) ON DELETE CASCADE,secret TEXT NOT NULL,status TEXT NOT NULL,expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS inbound(user_id TEXT REFERENCES users(id) ON DELETE CASCADE,message_id TEXT,PRIMARY KEY(user_id,message_id));
CREATE TABLE IF NOT EXISTS limits(key TEXT PRIMARY KEY,count INTEGER NOT NULL,until INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS migrations(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,manifest TEXT NOT NULL,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS workspaces(id TEXT PRIMARY KEY,user_id TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,legacy_source TEXT UNIQUE,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_instances(id TEXT PRIMARY KEY,user_id TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,created INTEGER NOT NULL,state TEXT NOT NULL DEFAULT 'reserved' CHECK(state IN ('reserved','active')));
''')
        if 'native_source' not in {r[1] for r in c.execute('PRAGMA table_info(workspaces)')}:
            c.execute('ALTER TABLE workspaces ADD COLUMN native_source TEXT')
        if 'state' not in {r[1] for r in c.execute('PRAGMA table_info(assistant_instances)')}:
            c.execute("ALTER TABLE assistant_instances ADD COLUMN state TEXT NOT NULL DEFAULT 'reserved'")
        c.execute('CREATE UNIQUE INDEX IF NOT EXISTS workspace_native_source ON workspaces(native_source) WHERE native_source IS NOT NULL')
        for row in c.execute('SELECT id FROM users WHERE id NOT IN (SELECT user_id FROM workspaces)').fetchall():
            c.execute('INSERT INTO workspaces(id,user_id,created) VALUES(?,?,?)',(ident(),row[0],now()))
    (DATA/'platform.sqlite3').chmod(0o600)

DEFAULT = dict(name='我的助手',address='',prompt='',style='简洁自然',timezone='Asia/Shanghai',routine_start='08:00',routine_end='23:30',weekly_schedule={},date_overrides={},modules={'memory':True,'tasks':True,'reminders':True,'expiry':True},provider='',model='',reasoning_effort='')
def workspace(uid):
    with db() as c:
        c.execute('INSERT OR IGNORE INTO workspaces(id,user_id,created) SELECT ?,id,? FROM users WHERE id=?',(ident(),now(),uid))
        row=c.execute('SELECT * FROM workspaces WHERE user_id=?',(uid,)).fetchone()
    if not row: raise ValueError('工作区不存在')
    return dict(row)
def public_workspace(uid):
    w=workspace(uid)
    source=native(uid)
    with db() as c:
        pending=c.execute("SELECT 1 FROM assistant_instances WHERE user_id=? AND state='reserved'",(uid,)).fetchone()
    return {'id':w['id'],'created':w['created'],'history_linked':bool(w['legacy_source']),'history_label':'旧 Friday' if w['legacy_source'] else '', 'native_mode':bool(source),'instance_managed':bool(source and source.startswith('instance:')), 'instance_pending':bool(pending and not w['native_source'] and not w['legacy_source'])}
def native(uid):
    with db() as c:
        r=c.execute("SELECT id FROM assistant_instances WHERE user_id=? AND state='active'",(uid,)).fetchone()
        if r:return 'instance:'+r[0]
        r=c.execute('SELECT native_source FROM workspaces WHERE user_id=?',(uid,)).fetchone()
        return r[0] if r else None
def settings(uid):
    with db() as c: r=c.execute('SELECT body,revision FROM settings WHERE user_id=?',(uid,)).fetchone()
    result=json.loads(json.dumps(DEFAULT))
    if r: result.update(json.loads(r['body']))
    return dict(result,revision=r['revision'] if r else 0)
def active(uid):
    with db() as c: return c.execute("SELECT 1 FROM users WHERE id=? AND status='active'",(uid,)).fetchone() is not None
def encrypt(v): return Fernet(key_file().read_bytes()).encrypt(json.dumps(v).encode()).decode()
def decrypt(v): return json.loads(Fernet(key_file().read_bytes()).decrypt(v.encode()))
def secret(uid,kind):
    with db() as c: r=c.execute('SELECT value FROM secrets WHERE user_id=? AND kind=?',(uid,kind)).fetchone()
    return decrypt(r[0]) if r else None
def put_secret(uid,kind,value):
    with db() as c: c.execute('INSERT OR REPLACE INTO secrets VALUES(?,?,?)',(uid,kind,encrypt(value)))
def event(uid,event,status='success'):
    with db() as c:
        if c.execute('SELECT 1 FROM users WHERE id=?',(uid,)).fetchone(): c.execute('INSERT INTO activity(user_id,event,status,created) VALUES(?,?,?,?)',(uid,event,status,now()))
def message(uid,role,text,source):
    with db() as c: c.execute('INSERT INTO messages(user_id,role,text,source,created) VALUES(?,?,?,?,?)',(uid,role,text[:32000],source,now()))
def entities(uid,kind):
    with db() as c: rows=c.execute('SELECT * FROM entities WHERE user_id=? AND kind=? ORDER BY created DESC',(uid,kind)).fetchall()
    return [dict(json.loads(r['body']),id=r['id'],revision=r['revision'],created=r['created']) for r in rows]
def limited(key,max_count=10,seconds=60):
    with db() as c:
        c.execute('BEGIN IMMEDIATE')
        c.execute('DELETE FROM limits WHERE until<?',(now(),))
        r=c.execute('SELECT count FROM limits WHERE key=?',(key,)).fetchone()
        if r and r[0]>=max_count: return True
        c.execute('INSERT INTO limits VALUES(?,1,?) ON CONFLICT(key) DO UPDATE SET count=count+1',(key,now()+seconds))
    return False
