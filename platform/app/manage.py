"""Server-side bootstrap: outputs are secrets, redirect into a protected file."""
import os, re, sys, secrets
from argon2 import PasswordHasher
from . import store as s
def main():
    s.init()
    if len(sys.argv)!=2 or sys.argv[1]!='bootstrap': raise SystemExit('usage: python -m app.manage bootstrap')
    login=os.environ.get('ASROBOT_BOOTSTRAP_LOGIN','admin')
    if not re.fullmatch(r'[A-Za-z0-9_.-]{1,64}',login): raise SystemExit('Invalid administrator login')
    with s.db() as c:
        if c.execute("SELECT 1 FROM users WHERE role='admin'").fetchone(): raise SystemExit('An administrator already exists; bootstrap refused')
        password=secrets.token_urlsafe(24)
        c.execute('INSERT INTO users(id,login,password,role,must_change,created) VALUES(?,?,?,?,1,?)',(s.ident(),login,PasswordHasher().hash(password),'admin',s.now()))
    print('用户名：'+login+'\n临时密码：'+password+'\n首次登录必须修改密码。此文件请在设置完成后妥善销毁。')
if __name__=='__main__': main()
