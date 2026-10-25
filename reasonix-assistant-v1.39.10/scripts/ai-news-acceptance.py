#!/usr/bin/env python3
"""Real binary + synthetic source snapshots, mock model and Weixin. No real mail/model credentials."""
import argparse, datetime as dt, hashlib, json, os, queue, signal, sqlite3, subprocess, tempfile, threading, time
from pathlib import Path
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    ap = argparse.ArgumentParser(); ap.add_argument('binary'); a = ap.parse_args()
    binary = str(Path(a.binary).resolve())
    with tempfile.TemporaryDirectory(prefix='mybot-ai-news-acceptance-') as tmp:
        home, work = Path(tmp) / 'home', Path(tmp) / 'work'
        home.mkdir(); work.mkdir()
        incoming = queue.Queue(); sent = []; requests = []; errors = []; source_calls = 0

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args): pass
            def reply(self, value):
                data = json.dumps(value).encode(); self.send_response(200)
                self.send_header('Content-Type', 'application/json'); self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                try: self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError): pass
            def do_POST(self):
                nonlocal source_calls
                body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))) or b'{}')
                if self.path.endswith('/getupdates'):
                    try: messages = [incoming.get(timeout=.15)]
                    except queue.Empty: messages = []
                    return self.reply({'ret': 0, 'errcode': 0, 'msgs': messages, 'get_updates_buf': 'fixture'})
                if self.path.endswith('/sendmessage'):
                    sent.append(body); return self.reply({'ret': 0, 'errcode': 0, 'message_id': str(len(sent))})
                if '/ilink/' in self.path: return self.reply({'ret': 0, 'errcode': 0, 'typing_ticket': 'fixture'})
                requests.append(body)
                messages = body.get('messages', [])
                rendered = json.dumps(messages, ensure_ascii=False)
                tools = {t.get('function', {}).get('name') for t in body.get('tools', [])}
                background = '独立AI News后台输出契约' in rendered
                def tc(ident, arguments):
                    return {'tool_calls': [{'index': 0, 'id': ident, 'type': 'function', 'function': {'name': 'assistant_ai_news', 'arguments': json.dumps(arguments)}}]}
                if background:
                    if 'LONG_PREF_MARKER' not in rendered or 'ACTIVE_SHORT_MARKER' not in rendered or 'EXPIRED_BODY_MARKER' in rendered:
                        errors.append('active/expired memory boundary wrong')
                    if tools - {'assistant_ai_news', 'assistant_memory', 'assistant_tasks', 'assistant_history'}:
                        errors.append('background has extra tools')
                    if not any(m.get('role') == 'tool' and 'candidates' in str(m.get('content', '')) for m in messages):
                        source_calls += 1; delta = tc('list', {'action': 'list'}); finish = 'tool_calls'
                    elif not any(m.get('role') == 'tool' and 'articles' in str(m.get('content', '')) for m in messages):
                        delta = tc('read', {'action': 'read', 'snapshot_id': 'fixture-news', 'ids': ['n'+str(i) for i in range(1,8)]}); finish = 'tool_calls'
                    elif not any(m.get('role') == 'tool' and 'max_characters' in str(m.get('content', '')) for m in messages):
                        delta = tc('check', {'action': 'check', 'events': [{'ids': ['n'+str(i)], 'title': '模型更新'+str(i), 'summary': '来源介绍了新模型的功能，相关结果仍需在真实任务中验证。'} for i in range(1,8)]}); finish = 'tool_calls'
                    else:
                        output = {'events': [{'ids': ['n'+str(i)], 'title': '模型更新'+str(i), 'summary': '来源介绍了新模型的功能，相关结果仍需在真实任务中验证。'} for i in range(1,8)], 'note': '补充说明：partial，某来源RSS超时，内部排序说明。'}
                        assert all(len(e['title'])+len(e['summary']) <= 50 for e in output['events'])
                        delta = {'content': json.dumps(output, ensure_ascii=False)}; finish = 'stop'
                elif not any(m.get('role') == 'tool' and 'output' in str(m.get('content', '')) for m in messages):
                    delta = tc('latest', {'action': 'latest'}); finish = 'tool_calls'
                else:
                    if 'https://example.com/n2' not in rendered: errors.append('follow-up lost original second news reference')
                    delta = {'content': 'FOLLOWUP_SOURCE_OK\nhttps://www.forbes.com/sites/thomasbrewster/2026/10/23/inside-openais-plan-to-protect-the-internet-before-hackers-break-it'}; finish = 'stop'
                data = ('data: ' + json.dumps({'choices': [{'delta': delta, 'finish_reason': finish}]}, ensure_ascii=False) + '\n\ndata: [DONE]\n\n').encode()
                self.send_response(200); self.send_header('Content-Type', 'text/event-stream'); self.send_header('Content-Length', str(len(data))); self.end_headers(); self.wfile.write(data)

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        base = 'http://127.0.0.1:' + str(server.server_port)
        (home / 'config.toml').write_text(f'''
default_model = "fixture/model"
[permissions]
mode = "ask"
allow = ["assistant_tasks", "assistant_memory", "assistant_ai_news", "assistant_heartbeat"]
[[providers]]
name = "fixture"
kind = "openai"
base_url = "{base}/v1"
models = ["model"]
default = "model"
api_key_env = "NEWS_FIXTURE_KEY"
[bot]
enabled = true
model = "fixture/model"
tool_approval_mode = "read-only"
debounce_ms = 0
[bot.pairing]
enabled = false
[bot.allowlist]
enabled = true
allow_all = false
weixin_users = ["fixture-owner"]
weixin_admins = ["fixture-owner"]
weixin_approvers = ["fixture-owner"]
[bot.weixin]
enabled = true
final_only = true
account_id = "fixture"
token_env = "NEWS_FIXTURE_WEIXIN"
api_base = "{base}"
''')
        env = dict(os.environ, REASONIX_HOME=str(home), HOME=str(home), REASONIX_ASSISTANT='1', TZ='Asia/Shanghai', NEWS_FIXTURE_KEY='fixture-key', NEWS_FIXTURE_WEIXIN='fixture-token', DO_NOT_TRACK='1')
        def cli(*args):
            p = subprocess.run([binary, 'assistant', *args], cwd=work, env=env, capture_output=True, text=True, timeout=20)
            if p.returncode: raise RuntimeError(p.stderr[-1000:])
            return json.loads(p.stdout)
        zone = dt.timezone(dt.timedelta(hours=8)); now = dt.datetime.now(zone)
        trigger = (now + dt.timedelta(seconds=5)).replace(microsecond=0)
        if trigger.date() != now.date(): raise RuntimeError('run acceptance away from midnight')
        stamp = lambda x: x.isoformat(timespec='seconds')
        cli('memory', json.dumps({'action': 'create', 'kind': 'long', 'subject': 'pref', 'body': 'LONG_PREF_MARKER'}))
        for index, (body, expires) in enumerate([('ACTIVE_SHORT_MARKER', trigger + dt.timedelta(hours=1)), ('EXPIRED_BODY_MARKER', trigger - dt.timedelta(seconds=2))]):
            cli('memory', json.dumps({'action': 'create', 'kind': 'short', 'subject': 'temporary-' + str(index), 'body': body, 'starts_at': stamp(now - dt.timedelta(hours=1)), 'expires_at': stamp(expires)}))
        cfg = cli('heartbeat-init')
        week = {d: {'enabled': True, 'start': trigger.strftime('%H:%M'), 'end': '24:00'} for d in ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday']}
        cfg = cli('heartbeat', json.dumps({'action': 'set_weekly', 'revision': cfg['revision'], 'weekly_schedule': week}))['config']
        for ident in ['morning', 'daytime_mail']:
            cfg = cli('heartbeat', json.dumps({'action': 'set_trigger', 'revision': cfg['revision'], 'trigger_id': ident, 'enabled': False}))['config']
        cfg = cli('heartbeat', json.dumps({'action': 'set_module_enabled', 'revision': cfg['revision'], 'module_id': 'ai_news_briefing', 'enabled': True}))['config']
        cfg = cli('heartbeat', json.dumps({'action': 'set_trigger', 'revision': cfg['revision'], 'trigger_id': 'news_test', 'mode': 'once', 'at': stamp(trigger), 'catch_up_seconds': 300, 'modules': ['ai_news_briefing']}))['config']
        occurrence = 'hb-' + hashlib.sha256(b'news_test:once').hexdigest()[:32]; job_id = occurrence + '-ai_news_briefing'
        articles = [{'id': 'n'+str(i), 'title': 'OpenAI model '+str(i), 'url': 'https://example.com/n'+str(i), 'evidence_url': 'https://example.com/n'+str(i), 'sources': ['openai'], 'published_at': stamp(now), 'time_basis': 'source_publication', 'text': 'Verified original source evidence and specific limitations. ' * 50, 'content_status': 'source_excerpt', 'metrics_at': stamp(now), 'rank_score': 10-i} for i in range(1,8)]
        snap = {'id': 'fixture-news', 'since': stamp(now-dt.timedelta(hours=24)), 'as_of': stamp(now), 'status': 'ok', 'ranking': 'fixture', 'sources': [], 'candidates': articles, 'target_count': 7}
        database = home / 'assistant' / 'state.sqlite3'
        with sqlite3.connect(database) as db:
            for kind, ident, value in [('ai_news_snapshot', 'fixture-news', snap), ('ai_news_cache', 'v1', {'snapshot_id': 'fixture-news', 'at': stamp(now)})]:
                db.execute('INSERT INTO assistant_entities(kind,id,payload) VALUES(?,?,?)', (kind, ident, json.dumps(value)))
        log = open(home / 'bot.log', 'w+'); command = [binary, 'bot', 'start', '--channels', 'weixin', '--dir', str(work), '--heartbeat', '--heartbeat-poll', '1s']
        process = subprocess.Popen(command, cwd=work, env=env, stdout=log, stderr=log, start_new_session=True)
        try:
            deadline = trigger.timestamp() + 60; durable = False
            while time.time() < deadline:
                with sqlite3.connect(database) as db:
                    row = db.execute("SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", (job_id,)).fetchone(); saved = json.loads(row[0]) if row else {}
                durable = bool(saved.get('history') and saved['history'][-1]['status'] == 'delivered')
                if durable: break
                if process.poll() is not None: raise RuntimeError('Bot exited')
                time.sleep(.2)
            assert durable and len(sent) == 1 and source_calls == 1, 'not exactly one delivered morning'
            text = json.dumps(sent, ensure_ascii=False)
            assert 'AI News' in text and '7个AI热点事件' in text
            assert all('模型更新'+str(i) in text for i in range(1,8)), 'not all seven summaries delivered'
            assert all(x not in text for x in ['补充说明', 'partial', 'RSS超时', '截至北京时间']), 'operational appendix leaked'
            before_followup = len(sent)
            incoming.put({'message_id': 'follow-up', 'from_user_id': 'fixture-owner', 'to_user_id': 'fixture', 'msg_type': 1, 'context_token': 'fixture', 'item_list': [{'type': 1, 'text_item': {'text': '独立AI News第二条展开'}}]})
            deadline = time.time() + 25
            while time.time() < deadline and 'FOLLOWUP_SOURCE_OK' not in json.dumps(sent): time.sleep(.2)
            assert 'FOLLOWUP_SOURCE_OK' in json.dumps(sent), 'follow-up did not resolve source'
            replies = [x['msg']['item_list'][0]['text_item']['text'] for x in sent[before_followup:]]
            assert len(replies) == 1 and replies[0].endswith('inside-openais-plan-to-protect-the-internet-before-hackers-break-it'), 'trailing URL was split into another bubble'
            os.killpg(process.pid, signal.SIGTERM); process.wait(timeout=15)
            count = len(sent); model_count = len(requests)
            process = subprocess.Popen(command, cwd=work, env=env, stdout=log, stderr=log, start_new_session=True)
            time.sleep(3); os.killpg(process.pid, signal.SIGTERM); process.wait(timeout=15)
            assert len(sent) == count and len(requests) == model_count, 'restart duplicated morning'
            assert not errors, errors
            print(json.dumps({'passed': True, 'actual_binary': True, 'standalone_news_messages': 1, 'fake_weixin_messages': len(sent), 'active_long_and_short_memory_verified_in_provider_request': True, 'expired_memory_body_absent': True, 'background_tool_scope_verified': True, 'seven_summaries_title_plus_body_at_most_50_characters_with_links': True, 'follow_up_original_source': True, 'final_followup_trailing_url_in_one_message': True, 'restart_duplicate_count': 0, 'mock_model_requests': len(requests), 'real_model_requests': 0, 'real_weixin_messages': 0, 'sources': 'synthetic seven-event snapshot; live RSS/API collection separately checked; no real model or Weixin calls'}, indent=2))
        except Exception:
            log.flush(); log.seek(0); print(log.read()[-7000:]); raise
        finally:
            if process.poll() is None: os.killpg(process.pid, signal.SIGTERM); process.wait(timeout=15)
            server.shutdown(); log.close()


if __name__ == '__main__': main()
