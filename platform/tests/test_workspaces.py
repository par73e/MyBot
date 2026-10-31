import asyncio, io, json, sqlite3, zipfile
import pytest
from test_platform import clients
from app import store as s, runtime as rt, history

def linked(client):
    uid=client.get('/asrobot/api/me').json()['user']['id']
    w=s.workspace(uid)
    with s.db() as c:c.execute("UPDATE workspaces SET legacy_source='friday_owner' WHERE user_id=?",(uid,))
    return uid,w

def test_stable_workspace_unique_per_user_and_restart(clients):
    a,b=clients
    wa=a.get('/asrobot/api/workspace').json();wb=b.get('/asrobot/api/workspace').json()
    assert wa['id']!=wb['id'] and not wa['history_linked']
    s.init()
    assert a.get('/asrobot/api/workspace').json()==wa
    assert b.get('/asrobot/api/workspace').json()==wb

def test_archive_access_resolves_authenticated_workspace_only(clients,monkeypatch):
    a,b=clients;uid,_=linked(a);seen=[]
    async def fake(owner,endpoint,**params):
        seen.append((owner,endpoint,params))
        return {'date':'2030-01-01','messages':[{'role':'user','text':'PRIVATE_ARCHIVE'}],'has_more':False,'next_cursor':''}
    monkeypatch.setattr(history,'archive',fake)
    r=a.get('/asrobot/api/messages?source=friday_archive&date=2030-01-01')
    assert r.status_code==200 and 'PRIVATE_ARCHIVE' in r.text
    assert b.get('/asrobot/api/messages?source=friday_archive&user_id='+uid).status_code==404
    assert 'PRIVATE_ARCHIVE' not in b.get('/asrobot/api/messages').text
    assert len(seen)==1 and seen[0][0]==uid
    wb=s.workspace(b.get('/asrobot/api/me').json()['user']['id'])
    with pytest.raises(sqlite3.IntegrityError),s.db() as c:
        c.execute("UPDATE workspaces SET legacy_source='friday_owner' WHERE id=?",(wb['id'],))

def test_archive_failure_visible_and_local_still_available(clients,monkeypatch):
    a,_=clients;linked(a)
    async def offline(*args,**kwargs):raise history.ArchiveUnavailable('归档服务不可用')
    monkeypatch.setattr(history,'archive',offline)
    d=a.get('/asrobot/api/conversations').json()
    assert d['workspace']['history_linked'] and d['archive_error']
    assert a.get('/asrobot/api/messages?source=friday_archive').status_code==503
    assert a.get('/asrobot/api/messages?source=local').status_code==200

def test_older_history_page_date_query_finds_archive_without_source_picker(clients,monkeypatch):
    a,b=clients;uid,_=linked(a)
    async def fake(owner,endpoint,**params):
        assert owner==uid and params['date']=='2026-10-27'
        return {'date':'2026-10-27','messages':[{'id':'owned:1','role':'user','text':'OLD_FRIDAY_HISTORY','created':0,'source':'friday_archive'}],'has_more':False,'next_before':0}
    monkeypatch.setattr(history,'archive',fake)
    assert 'OLD_FRIDAY_HISTORY' in a.get('/asrobot/api/messages?date=2026-10-27&before=0').text
    assert 'OLD_FRIDAY_HISTORY' not in b.get('/asrobot/api/messages?date=2026-10-27&before=0').text

def test_archive_index_and_export_do_not_copy_bodies(clients,monkeypatch):
    a,_=clients;uid,w=linked(a)
    async def fake(owner,endpoint,**params):
        assert endpoint=='days'
        return {'conversations':[{'date':'2030-01-01','source':'friday_archive'}]}
    monkeypatch.setattr(history,'archive',fake)
    d=a.get('/asrobot/api/conversations').json()
    assert d['conversations']==[{'date':'2030-01-01','source':'friday_archive'}]
    with zipfile.ZipFile(io.BytesIO(a.post('/asrobot/api/export',json={}).content)) as z:
        data=json.loads(z.read('asrobot.json'))
        assert data['workspace']['id']==w['id'] and data['workspace']['history_linked']
        assert '旧 Friday' in data['manifest']['archive_scope']
        assert data['messages']==[]
    with s.db() as c:assert c.execute('SELECT count(*) FROM messages WHERE user_id=?',(uid,)).fetchone()[0]==0

def test_assistant_queries_linked_archive_as_bounded_reference(clients,monkeypatch):
    a,b=clients;uid,_=linked(a);seen=[]
    async def fake(owner,endpoint,**params):
        seen.append(owner)
        return {'messages':[{'role':'assistant','text':'x'*3000,'source':'friday_archive'}],'has_more':True,'next_cursor':'opaque'}
    monkeypatch.setattr(history,'archive',fake)
    page=asyncio.run(rt.read_history(uid,{'date':'2030-01-01','query':'x'}))
    section=page['conversations'][1]
    assert page['reference_only'] and section['has_more'] and section['next_cursor']=='opaque'
    assert len(section['messages'][0]['text'])==2000 and section['messages'][0]['text_truncated']
    other=b.get('/asrobot/api/me').json()['user']['id']
    assert len(asyncio.run(rt.read_history(other,{}))['conversations'])==1
    assert seen==[uid]

def test_demotion_updates_live_session_and_workspace_delete_cascades(clients):
    a,b=clients;uid,w=linked(a)
    with s.db() as c:c.execute("UPDATE users SET role='user' WHERE id=?",(uid,))
    assert a.get('/asrobot/api/me').json()['user']['role']=='user'
    assert a.get('/asrobot/api/admin/users').status_code==403
    assert a.get('/asrobot/api/workspace').json()['id']==w['id']
    assert a.request('DELETE','/asrobot/api/account',json={'confirm':'alice','password':'test-password-123'}).status_code==200
    with s.db() as c:assert c.execute('SELECT 1 FROM workspaces WHERE user_id=?',(uid,)).fetchone() is None
