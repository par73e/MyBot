"""Recoverable owner-bound installation of a confirmed WeChat pairing."""
from . import native, runtime as rt, store as s


async def complete(user_id, pairing_id, result=None):
    """Reserve ownership, install in the instance, then commit confirmation.

    A failed or interrupted installation remains encrypted and retryable. It is
    never reported as confirmed until the private bridge has accepted it.
    Caller holds the per-user runtime lock.
    """
    source = s.native(user_id)
    if not source or not source.startswith('instance:'):
        raise ValueError('当前账号没有独立助手实例')
    with s.db() as db:
        db.execute('BEGIN IMMEDIATE')
        user = db.execute('SELECT status FROM users WHERE id=?', (user_id,)).fetchone()
        pairing = db.execute('SELECT status,expires FROM pairings WHERE id=? AND user_id=?', (pairing_id, user_id)).fetchone()
        if not user or user['status'] != 'active' or not pairing:
            raise ValueError('配对已取消或账号不可用')
        if pairing['status'] == 'confirmed':
            return {'status':'confirmed'}
        if pairing['status'] == 'installing':
            row = db.execute("SELECT value FROM secrets WHERE user_id=? AND kind='native_pairing_pending'", (user_id,)).fetchone()
            if not row:
                raise ValueError('配对状态需要人工核对')
            binding = s.decrypt(row['value'])
        else:
            if pairing['expires'] <= s.now() or not isinstance(result, dict):
                raise ValueError('二维码已过期，请重新申请')
            # The legacy runtime is not represented in the platform bindings
            # table. Until its ownership has been migrated, we cannot prove a
            # newly scanned bot is different from that running legacy bot.
            if db.execute('SELECT 1 FROM workspaces WHERE native_source IS NOT NULL LIMIT 1').fetchone():
                raise ValueError('原助手的微信归属尚未迁移，请先完成归属核对')
            if not all(isinstance(result.get(k), str) and result[k] for k in ('bot_token','ilink_bot_id','ilink_user_id')):
                raise ValueError('微信返回的身份信息不完整')
            binding = {'account':result['ilink_bot_id'], 'user':result['ilink_user_id'], 'token':result['bot_token'], 'base':rt.wx_url(result.get('baseurl') or rt.WX)}
            existing = db.execute('SELECT external_id FROM bindings WHERE user_id=?', (user_id,)).fetchone()
            if existing and existing['external_id'] != binding['account']:
                raise ValueError('已有微信绑定，需要先处理原绑定')
            other = db.execute('SELECT user_id FROM bindings WHERE external_id=?', (binding['account'],)).fetchone()
            if other and other['user_id'] != user_id:
                raise ValueError('该微信助手已绑定其他平台账号')
            db.execute("INSERT INTO bindings(user_id,external_id,status) VALUES(?,?,'installing') ON CONFLICT(user_id) DO UPDATE SET status='installing'", (user_id, binding['account']))
            db.execute("INSERT OR REPLACE INTO secrets(user_id,kind,value) VALUES(?,?,?)", (user_id,'native_pairing_pending',s.encrypt(binding)))
            db.execute("UPDATE pairings SET status='installing',secret=? WHERE id=? AND user_id=?", (s.encrypt({}),pairing_id,user_id))
    # The pending record is durable before crossing the process boundary.
    status = await native.request(user_id, 'status')
    if status.get('running') is not False:
        raise ValueError('请先停止该助手的后台进程，再完成微信配对')
    response = await native.request(user_id, 'pair-wechat', binding)
    if response.get('ok') is not True:
        raise ValueError('微信绑定尚未完成，请重试')
    with s.db() as db:
        db.execute('BEGIN IMMEDIATE')
        user = db.execute('SELECT status FROM users WHERE id=?',(user_id,)).fetchone()
        pairing = db.execute('SELECT status FROM pairings WHERE id=? AND user_id=?',(pairing_id,user_id)).fetchone()
        if not user or user['status'] != 'active' or not pairing or pairing['status'] != 'installing':
            raise ValueError('配对状态发生变化，需要核对后重试')
        db.execute("UPDATE bindings SET status='bound' WHERE user_id=? AND external_id=?", (user_id,binding['account']))
        db.execute("DELETE FROM secrets WHERE user_id=? AND kind='native_pairing_pending'", (user_id,))
        db.execute("UPDATE pairings SET status='confirmed',secret=? WHERE id=? AND user_id=?", (s.encrypt({}),pairing_id,user_id))
    s.event(user_id,'微信配对成功')
    return {'status':'confirmed', 'assistant_started':False}
