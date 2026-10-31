"""An operator attaches the original owner; no public API can choose a runtime."""
import sys, os
from . import store as s
def main():
    expected=os.environ.get('ASROBOT_LEGACY_OWNER_LOGIN')
    if not expected or len(sys.argv)!=2 or sys.argv[1]!=expected:raise SystemExit('Explicit matching legacy owner required')
    s.init()
    with s.db() as c:
        c.execute('BEGIN IMMEDIATE')
        user=c.execute('SELECT id,role,status FROM users WHERE login=?',(expected,)).fetchone()
        if not user or user['role']!='user' or user['status']!='active':raise SystemExit('Active ordinary owner required')
        uid=user['id'];w=c.execute('SELECT * FROM workspaces WHERE user_id=?',(uid,)).fetchone()
        if not w or w['legacy_source']!='friday_owner':raise SystemExit('Existing original history ownership required')
        if c.execute('SELECT 1 FROM bindings WHERE user_id=?',(uid,)).fetchone():raise SystemExit('Independent platform WeChat binding exists; explicit reconciliation required')
        if c.execute('SELECT 1 FROM entities WHERE user_id=?',(uid,)).fetchone():raise SystemExit('Platform business records exist; explicit reconciliation required')
        c.execute("UPDATE workspaces SET native_source='friday_full' WHERE user_id=?",(uid,))
    s.event(uid,'完整原 Friday 运行环境归属当前工作区；未复制或重置配置')
    print('Original assistant attached to the verified owner; no source credentials copied')
if __name__=='__main__':main()
