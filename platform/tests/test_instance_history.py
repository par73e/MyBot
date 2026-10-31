import pytest
from app import native, store as s
from test_platform import clients


def test_instance_history_is_scoped_and_reassembles_long_messages(clients, monkeypatch):
    first, second = clients
    users = [client.get('/asrobot/api/me').json()['user']['id'] for client in clients]
    for uid, ident in zip(users, ('1'*32, '2'*32)):
        with s.db() as db:
            db.execute("INSERT INTO assistant_instances VALUES(?,?,?,'active')", (ident, uid, s.now()))
    async def fake(uid, endpoint, body=None):
        if endpoint == 'status': return {'wechat_configured': True}
        assert endpoint == 'tool' and body['tool'] == 'assistant_history'
        args = body['args']
        session_id = 'session-' + uid
        if args['action'] == 'list':
            return {'conversations':[{'date':'2026-10-31','session_id':session_id}]}
        assert args['session_id'] == session_id
        if args.get('text_offset'):
            messages = [{'message_id':'msg','role':'user','text':'-tail','created_at':1000}]
        else:
            messages = [{'message_id':'msg','role':'user','text':'private-'+uid,'created_at':1000,'truncated':True,'next_text_offset':20}]
        return {'conversations':[{'status':'ready','messages':messages,'has_more':False}]}
    monkeypatch.setattr(native, 'request', fake)
    for client, uid, foreign in ((first, users[0], users[1]), (second, users[1], users[0])):
        index = client.get('/asrobot/api/conversations').json()
        assert any(item['source'] == 'instance' and item['date'] == '2026-10-31' for item in index['conversations'])
        response = client.get('/asrobot/api/messages?source=instance&date=2026-10-31')
        assert response.status_code == 200
        text = response.json()['messages'][0]['text']
        assert text == 'private-'+uid+'-tail' and foreign not in text


def test_instance_history_is_unavailable_to_unattached_user(clients):
    _, user = clients
    assert user.get('/asrobot/api/messages?source=instance&date=2026-10-31').status_code == 400
