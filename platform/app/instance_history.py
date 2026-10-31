"""Read an instance's own native history through its authenticated bridge."""
from . import native, store as s


async def _call(uid, args):
    source = s.native(uid)
    if not source or not source.startswith('instance:'):
        raise ValueError('当前工作区没有独立助手实例')
    return await native.request(uid, 'tool', {'tool': 'assistant_history', 'args': args})


def _day(item):
    return 'legacy' if item.get('legacy') else item.get('date', '')


async def days(uid):
    status = await native.request(uid, 'status')
    if not status.get('wechat_configured'):
        return []
    result, before = [], ''
    for _ in range(100):
        args = {'action': 'list'}
        if before:
            args['before_date'] = before
        page = await _call(uid, args)
        for item in page.get('conversations', []):
            day = _day(item)
            if day:
                result.append({'date': day, 'source': 'instance'})
        next_before = page.get('next_before_date', '')
        if not next_before:
            return result
        if next_before == before:
            raise ValueError('会话日期分页失败')
        before = next_before
    raise ValueError('会话日期过多，请缩小查询范围')


async def messages(uid, date, cursor=''):
    if not isinstance(cursor, str) or len(cursor) > 4096:
        raise ValueError('历史游标无效')
    args = {'action': 'list'}
    if date != 'legacy':
        args['date'] = date
    listing = await _call(uid, args)
    matches = [item for item in listing.get('conversations', []) if _day(item) == date]
    if not matches:
        return {'date': date, 'messages': [], 'has_more': False, 'next_cursor': ''}
    if len(matches) != 1 or not isinstance(matches[0].get('session_id'), str):
        raise ValueError('会话归属不明确')
    session_id = matches[0]['session_id']
    request = {'action': 'read', 'session_id': session_id, 'limit': 20}
    if cursor:
        request['cursor'] = cursor
    result = await _call(uid, request)
    sections = result.get('conversations', [])
    if len(sections) != 1 or sections[0].get('status') != 'ready':
        raise ValueError('会话索引仍在准备，请稍后重试')
    section = sections[0]
    output = []
    for message in section.get('messages', []):
        text = message.get('text', '')
        offset = message.get('next_text_offset', 0)
        for _ in range(130):
            if not offset:
                break
            fragment = await _call(uid, {'action': 'read', 'session_id': session_id, 'message_id': message['message_id'], 'text_offset': offset, 'limit': 1})
            pieces = fragment.get('conversations', [])
            if len(pieces) != 1 or pieces[0].get('status') != 'ready' or len(pieces[0].get('messages', [])) != 1:
                raise ValueError('长消息读取不完整，请稍后重试')
            part = pieces[0]['messages'][0]
            if part.get('message_id') != message.get('message_id'):
                raise ValueError('长消息归属不匹配')
            text += part.get('text', '')
            next_offset = part.get('next_text_offset', 0)
            if next_offset and next_offset <= offset:
                raise ValueError('长消息分页失败')
            offset = next_offset
        if offset or (message.get('truncated') and not message.get('next_text_offset')):
            text += '\n[消息过长，仅显示可用部分]'
        output.append({'id': message.get('message_id'), 'role': message.get('role'), 'text': text,
                       'created': message.get('created_at', 0) // 1000, 'source': 'wechat'})
    return {'date': date, 'messages': output, 'has_more': section.get('has_more', False), 'next_cursor': section.get('next_cursor', '')}
