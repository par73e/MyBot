"""Private operator command. No public API accepts archive aliases or paths."""
import sys
from . import store as s
def main():
    if len(sys.argv)!=2: raise SystemExit('usage: python -m app.link_history LOGIN')
    s.init()
    with s.db() as c: u=c.execute('SELECT id,role FROM users WHERE login=?',(sys.argv[1],)).fetchone()
    if not u or u['role']!='user': raise SystemExit('An ordinary user account is required')
    w=s.workspace(u['id'])
    with s.db() as c:
        c.execute('BEGIN IMMEDIATE')
        owner=c.execute("SELECT user_id FROM workspaces WHERE legacy_source='friday_owner'").fetchone()
        if owner and owner[0]!=u['id']: raise SystemExit('Archive already belongs to another workspace')
        c.execute("UPDATE workspaces SET legacy_source='friday_owner' WHERE user_id=?",(u['id'],))
    s.event(u['id'],'关联旧 Friday 会话索引（只读）')
    print('Linked owner history to workspace '+w['id']+'; no message bodies copied')
if __name__=='__main__':main()
