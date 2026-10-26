#!/usr/bin/env python3
"""Real binary + synthetic source snapshots, mock model and Weixin. No real mail/model credentials."""
import argparse, datetime as dt, hashlib, json, os, queue, signal, sqlite3, subprocess, tempfile, threading, time
from pathlib import Path
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    ap = argparse.ArgumentParser(); ap.add_argument('binary'); ap.add_argument('--scenario', choices=['normal', 'repair', 'fallback'], default='normal'); a = ap.parse_args()
    binary = str(Path(a.binary).resolve())
    with tempfile.TemporaryDirectory(prefix='mybot-morning-acceptance-') as tmp:
        home, work = Path(tmp) / 'home', Path(tmp) / 'work'
        home.mkdir(); work.mkdir()
        incoming = queue.Queue(); sent = []; requests = []; errors = []; source_calls = 0; final_drafts = 0; full_checks = 0

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args): pass
            def reply(self, value):
                data = json.dumps(value).encode(); self.send_response(200)
                self.send_header('Content-Type', 'application/json'); self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                try: self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError): pass
            def do_POST(self):
                nonlocal source_calls, final_drafts, full_checks
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
                background = '早安Friday输出契约' in rendered
                if background:
                    if 'LONG_PREF_MARKER' not in rendered or 'ACTIVE_SHORT_MARKER' not in rendered or 'EXPIRED_BODY_MARKER' in rendered:
                        errors.append('active/expired memory boundary wrong')
                    if tools - {'assistant_morning', 'assistant_weather', 'assistant_mail', 'assistant_memory', 'assistant_tasks', 'assistant_history'}:
                        errors.append('background has extra tools')
                    if not any(m.get('role') == 'tool' and 'mail_ids' in str(m.get('content', '')) for m in messages):
                        source_calls += 1
                        delta = {'tool_calls': [{'index': 0, 'id': 'snapshot', 'type': 'function', 'function': {'name': 'assistant_morning', 'arguments': '{"action":"snapshot"}'}}]}; finish = 'tool_calls'
                    else:
                        repairing = 'validation_error' in rendered
                        output = {'weather': '暂未获取', 'mail_decisions': [], 'news': [{'id': 'n1', 'summary': '模型A更新'}, {'id': 'n2', 'summary': '工具B开源'}, {'id': 'n3', 'summary': '芯片C发布'}]}
                        if a.scenario == 'fallback' or a.scenario == 'repair' and not repairing:
                            output['weather'] = '长' * 101
                        last_user = max(i for i, m in enumerate(messages) if m.get('role') == 'user')
                        tail = messages[last_user + 1:]
                        checked = any(m.get('role') == 'tool' and 'summary_lengths' in str(m.get('content', '')) for m in tail)
                        if not checked:
                            full_checks += 1
                            delta = {'tool_calls': [{'index': 0, 'id': 'check-' + str(full_checks), 'type': 'function', 'function': {'name': 'assistant_morning', 'arguments': json.dumps({'action': 'check', 'draft': output})}}]}; finish = 'tool_calls'
                        else:
                            # Deliberately ignore rejection in failure scenarios to
                            # exercise host repair and last-resort native delivery.
                            final_drafts += 1
                            delta = {'content': json.dumps(output, ensure_ascii=False)}; finish = 'stop'
                elif not any(m.get('role') == 'tool' and 'displayed_mail_ids' in str(m.get('content', '')) for m in messages):
                    delta = {'tool_calls': [{'index': 0, 'id': 'latest', 'type': 'function', 'function': {'name': 'assistant_morning', 'arguments': '{"action":"latest"}'}}]}; finish = 'tool_calls'
                else:
                    if 'https://example.com/n2' not in rendered: errors.append('follow-up lost original second news reference')
                    delta = {'content': 'FOLLOWUP_SOURCE_OK'}; finish = 'stop'
                data = ('data: ' + json.dumps({'choices': [{'delta': delta, 'finish_reason': finish}]}, ensure_ascii=False) + '\n\ndata: [DONE]\n\n').encode()
                self.send_response(200); self.send_header('Content-Type', 'text/event-stream'); self.send_header('Content-Length', str(len(data))); self.end_headers(); self.wfile.write(data)

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        base = 'http://127.0.0.1:' + str(server.server_port)
        (home / 'config.toml').write_text(f'''
default_model = "fixture/model"
[permissions]
mode = "ask"
allow = ["assistant_tasks", "assistant_memory", "assistant_morning", "assistant_heartbeat"]
[[providers]]
name = "fixture"
kind = "openai"
base_url = "{base}/v1"
models = ["model"]
default = "model"
api_key_env = "MORNING_FIXTURE_KEY"
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
account_id = "fixture"
token_env = "MORNING_FIXTURE_WEIXIN"
api_base = "{base}"
''')
        env = dict(os.environ, REASONIX_HOME=str(home), HOME=str(home), REASONIX_ASSISTANT='1', TZ='Asia/Shanghai', MORNING_FIXTURE_KEY='fixture-key', MORNING_FIXTURE_WEIXIN='fixture-token', DO_NOT_TRACK='1')
        def cli(*args):
            p = subprocess.run([binary, 'assistant', *args], cwd=work, env=env, capture_output=True, text=True, timeout=20)
            if p.returncode: raise RuntimeError(p.stderr[-1000:])
            return json.loads(p.stdout)
        zone = dt.timezone(dt.timedelta(hours=8)); now = dt.datetime.now(zone)
        trigger = (now + dt.timedelta(seconds=8)).replace(microsecond=0)
        if trigger.date() != now.date(): raise RuntimeError('run acceptance away from midnight')
        stamp = lambda x: x.isoformat(timespec='seconds')
        cli('memory', json.dumps({'action': 'create', 'kind': 'long', 'subject': 'pref', 'body': 'LONG_PREF_MARKER'}))
        for index, (body, expires) in enumerate([('ACTIVE_SHORT_MARKER', trigger + dt.timedelta(hours=1)), ('EXPIRED_BODY_MARKER', trigger - dt.timedelta(seconds=2))]):
            cli('memory', json.dumps({'action': 'create', 'kind': 'short', 'subject': 'temporary-' + str(index), 'body': body, 'starts_at': stamp(now - dt.timedelta(hours=1)), 'expires_at': stamp(expires)}))
        cfg = cli('morning-enable')
        week = {d: {'enabled': True, 'start': trigger.strftime('%H:%M'), 'end': '24:00'} for d in ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday']}
        cfg = cli('heartbeat', json.dumps({'action': 'set_weekly', 'revision': cfg['revision'], 'weekly_schedule': week}))['config']
        date = str(trigger.date()); occurrence = 'hb-' + hashlib.sha256(('morning:' + date).encode()).hexdigest()[:32]; job_id = occurrence + '-morning_briefing'
        job = {'id': job_id, 'title': '早安Friday', 'prompt': '必须读取数据，按早安Friday输出契约返回。', 'at': stamp(trigger), 'endsAt': stamp(trigger + dt.timedelta(minutes=4)), 'enabled': True, 'timezone': 'Asia/Shanghai', 'workspaceRoot': str(work), 'model': 'fixture/model', 'approvalMode': 'read-only', 'kind': 'agent', 'capability': 'morning', 'decide_notification': True, 'newConversationEachRun': True, 'module_id': 'morning_briefing', 'trigger_id': 'morning', 'occurrence_id': occurrence, 'trigger_revision': cfg['revision'], 'target': {'connection_id': 'weixin', 'domain': 'weixin', 'chat_id': 'fixture-owner', 'chat_type': 'dm'}}
        # The source snapshot is synthetic. Real mail delta/body reading is tested separately in Go.
        news = [{'id': 'n'+str(i), 'title': 'Synthetic AI model '+str(i), 'url': 'https://example.com/n'+str(i), 'evidence_url': 'https://example.com/n'+str(i), 'sources': ['openai'], 'published_at': stamp(now), 'time_basis': 'source_publication', 'text': 'Synthetic verified source evidence and limitations. ' * 50, 'content_status': 'original_excerpt', 'read_at': stamp(now), 'rank_score': 10-i} for i in range(1,8)]
        snap = {'id': 'fixture-shared-news', 'since': stamp(now-dt.timedelta(hours=24)), 'as_of': stamp(now), 'status': 'partial', 'ranking': 'source_consensus_and_hn_discussion_v1', 'sources': [], 'candidates': news, 'target_count': 7}
        record = {'id': occurrence, 'trigger_id': 'morning', 'date': date, 'revision': cfg['revision'], 'scheduled_at': stamp(trigger), 'modules': ['morning_briefing'], 'job_ids': [job_id]}
        database = home / 'assistant' / 'state.sqlite3'
        with sqlite3.connect(database) as db:
            for kind, ident, value in [('job', job_id, job), ('module_occurrence', occurrence, record), ('ai_news_snapshot', 'fixture-shared-news', snap), ('ai_news_cache', 'v1', {'snapshot_id': 'fixture-shared-news', 'at': stamp(now)})]: db.execute('INSERT INTO assistant_entities(kind,id,payload) VALUES(?,?,?)', (kind, ident, json.dumps(value)))
        log = open(home / 'bot.log', 'w+'); command = [binary, 'bot', 'start', '--channels', 'weixin', '--dir', str(work), '--heartbeat', '--heartbeat-poll', '1s']
        process = subprocess.Popen(command, cwd=work, env=env, stdout=log, stderr=log, start_new_session=True)
        try:
            deadline = trigger.timestamp() + 45; durable = False
            while time.time() < deadline:
                with sqlite3.connect(database) as db:
                    saved = json.loads(db.execute("SELECT payload FROM assistant_entities WHERE kind='job' AND id=?", (job_id,)).fetchone()[0])
                durable = bool(saved.get('history') and saved['history'][-1]['status'] == 'delivered')
                if durable: break
                if process.poll() is not None: raise RuntimeError('Bot exited')
                time.sleep(.2)
            assert durable and len(sent) == 1 and source_calls == 1, 'not exactly one delivered morning: ' + json.dumps({'sent': len(sent), 'source_calls': source_calls, 'saved': saved}, ensure_ascii=False)
            text = json.dumps(sent, ensure_ascii=False)
            assert 'Sir，早上好。' in text and '芯片C发布' in text
            assert final_drafts == (1 if a.scenario == 'normal' else 2), 'unexpected generation/repair count: ' + json.dumps({'drafts': final_drafts, 'checks': full_checks, 'requests': len(requests), 'repair_requests': sum('validation_error' in json.dumps(r) for r in requests), 'history': saved.get('history')}, ensure_ascii=False)
            with sqlite3.connect(database) as db:
                delivered = json.loads(db.execute("SELECT payload FROM assistant_entities WHERE kind='morning_delivered' AND id=?", (job_id,)).fetchone()[0])
            assert bool(delivered.get('fallback')) == (a.scenario == 'fallback'), 'wrong fallback status'
            if a.scenario == 'fallback':
                assert ('邮件还没整理好' in text or '邮件自动检查还暂停着' in text) and delivered['displayed_mail_ids'] == []
            assert all(label not in text for label in ['天气：', '课程：', '邮件：', '待办：', 'AI News：'])
            lines = sent[0]['msg']['item_list'][0]['text_item']['text'].split('\n')
            assert len(lines) == 6 and lines[0] == '# **Sir，早上好。**' and all(line.count('。') == 1 for line in lines), 'expected greeting and five single-sentence lines'
            incoming.put({'message_id': 'follow-up', 'from_user_id': 'fixture-owner', 'to_user_id': 'fixture', 'msg_type': 1, 'context_token': 'fixture', 'item_list': [{'type': 1, 'text_item': {'text': '第二条AI News展开'}}]})
            deadline = time.time() + 25
            while time.time() < deadline and 'FOLLOWUP_SOURCE_OK' not in json.dumps(sent): time.sleep(.2)
            assert 'FOLLOWUP_SOURCE_OK' in json.dumps(sent), 'follow-up did not resolve source'
            os.killpg(process.pid, signal.SIGTERM); process.wait(timeout=15)
            count = len(sent); model_count = len(requests)
            process = subprocess.Popen(command, cwd=work, env=env, stdout=log, stderr=log, start_new_session=True)
            time.sleep(3); os.killpg(process.pid, signal.SIGTERM); process.wait(timeout=15)
            assert len(sent) == count and len(requests) == model_count, 'restart duplicated morning'
            assert not errors, errors
            print(json.dumps({'passed': True, 'scenario': a.scenario, 'final_drafts': final_drafts, 'full_draft_checks': full_checks, 'fallback': a.scenario == 'fallback', 'actual_binary': True, 'proactive_morning_messages': 1, 'fake_weixin_messages': len(sent), 'active_long_and_short_memory_verified_in_provider_request': True, 'expired_memory_body_absent': True, 'background_tool_scope_verified': True, 'greeting_plus_five_lines_without_headings': True, 'native_shared_collector_snapshot_to_top_three': True, 'follow_up_original_source': True, 'restart_duplicate_count': 0, 'mock_model_requests': len(requests), 'real_model_requests': 0, 'real_weixin_messages': 0, 'sources': 'synthetic shared seven-candidate news cache; native morning prepare selects/reads top three; mock model and Weixin, no real services'}, indent=2))
        except Exception:
            log.flush(); log.seek(0); print(log.read()[-7000:]); raise
        finally:
            if process.poll() is None: os.killpg(process.pid, signal.SIGTERM); process.wait(timeout=15)
            server.shutdown(); log.close()


if __name__ == '__main__': main()
