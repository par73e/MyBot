#!/usr/bin/env python3
"""Actual bot/child-agent/native-history acceptance against local fake APIs only."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import queue
import signal
import sqlite3
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('binary')
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix='friday-daily-acceptance-') as tmp:
        home, work = Path(tmp) / 'home', Path(tmp) / 'work'
        home.mkdir(); work.mkdir()
        incoming, sent, requests, errors = queue.Queue(), [], [], []
        now = dt.datetime.now(dt.timezone(dt.timedelta(hours=8)))
        day = (now - dt.timedelta(hours=6)).date().isoformat()
        tool_calls = 0

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_): pass

            def reply(self, value):
                body = json.dumps(value).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                try: self.wfile.write(body)
                except (BrokenPipeError, ConnectionResetError): pass

            def do_POST(self):
                nonlocal tool_calls
                body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))) or b'{}')
                if self.path.endswith('/getupdates'):
                    try: messages = [incoming.get(timeout=.15)]
                    except queue.Empty: messages = []
                    return self.reply({'ret': 0, 'errcode': 0, 'msgs': messages, 'get_updates_buf': 'daily-fixture'})
                if self.path.endswith('/sendmessage'):
                    sent.append(body)
                    return self.reply({'ret': 0, 'errcode': 0, 'message_id': str(len(sent))})
                if '/ilink/' in self.path:
                    return self.reply({'ret': 0, 'errcode': 0, 'typing_ticket': 'fixture'})
                if not self.path.endswith('/chat/completions'):
                    return self.reply({'error': 'unexpected path'})
                requests.append(body)
                messages = body.get('messages', [])
                user = next((str(m.get('content', '')) for m in reversed(messages) if m.get('role') == 'user'), '')
                finish = 'stop'
                if 'DAILY_AGENT' in user:
                    if 'daily_conversation_reference' not in user or 'DAILY_FIXED' not in user:
                        errors.append('background executor did not receive the current day notification context')
                    delta = {'content': json.dumps({'notify': True, 'reason': 'test', 'message': 'DAILY_REPORT 东京旅行安排'})}
                elif 'DAILY_SILENT' in user:
                    delta = {'content': json.dumps({'notify': False, 'reason': 'nothing actionable', 'message': ''})}
                elif 'CASE_HISTORY' in user:
                    tool_messages = [m for m in messages if m.get('role') == 'tool' and 'reference_only' in str(m.get('content', ''))]
                    latest = str(tool_messages[-1].get('content', '')) if tool_messages else ''
                    if not latest or '"status":"preparing"' in latest:
                        tool_calls += 1
                        if tool_calls > 8: errors.append('native history stayed preparing')
                        delta = {'tool_calls': [{'index': 0, 'id': 'history-' + str(tool_calls), 'type': 'function', 'function': {'name': 'assistant_history', 'arguments': json.dumps({'action': 'read', 'date': day})}}]}
                        finish = 'tool_calls'
                    else:
                        if 'DAILY_REPORT' not in latest or 'DAILY_FIXED' not in latest:
                            errors.append('native history omitted delivered proactive messages')
                        if 'notify' in latest or 'daily_conversation_reference' in latest:
                            errors.append('private job decision or host prompt leaked into daily history')
                        delta = {'content': 'HISTORY_OK'}
                else:
                    visible = '\n'.join(str(m.get('content', '')) for m in messages if m.get('role') == 'assistant')
                    if 'DAILY_REPORT' not in visible or 'DAILY_FIXED' not in visible:
                        errors.append('follow-up did not inherit daily proactive messages')
                    delta = {'content': 'FOLLOWUP_OK'}
                data = ('data: ' + json.dumps({'choices': [{'delta': delta, 'finish_reason': finish}]}) + '\n\ndata: [DONE]\n\n').encode()
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.send_header('Content-Length', str(len(data)))
                self.end_headers(); self.wfile.write(data)

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        base = 'http://127.0.0.1:' + str(server.server_port)
        (home / 'config.toml').write_text(f'''
default_model = "fixture/model"
[agent]
temperature = 0.0
[permissions]
mode = "ask"
allow = ["assistant_tasks", "assistant_memory", "assistant_history"]
[[providers]]
name = "fixture"
kind = "openai"
base_url = "{base}/v1"
models = ["model"]
default = "model"
api_key_env = "DAILY_FIXTURE_KEY"
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
token_env = "DAILY_FIXTURE_WEIXIN"
api_base = "{base}"
''')
        env = dict(os.environ, HOME=str(home), REASONIX_HOME=str(home), REASONIX_ASSISTANT='1', TZ='Asia/Shanghai', DAILY_FIXTURE_KEY='fixture', DAILY_FIXTURE_WEIXIN='fixture', REASONIX_TELEMETRY='off')
        subprocess.run([binary, 'assistant', 'context'], env=env, cwd=work, check=True, capture_output=True)
        database = home / 'assistant/state.sqlite3'
        with sqlite3.connect(database) as db:
            for index, (kind, prompt) in enumerate((('message', 'DAILY_FIXED'), ('agent', 'DAILY_AGENT'), ('agent', 'DAILY_SILENT'))):
                at = now + dt.timedelta(seconds=2 + index * 4)
                job = dict(id='daily-' + str(index), title='Daily fixture', prompt=prompt, at=at.isoformat(), timezone='Asia/Shanghai', enabled=True, workspaceRoot=str(work), model='fixture/model', approvalMode='read-only', kind=kind, newConversationEachRun=True, createdAt=int(now.timestamp() * 1000), target=dict(connection_id='weixin', domain='weixin', chat_id='fixture-owner', chat_type='dm'), decide_notification=kind == 'agent')
                db.execute("INSERT INTO assistant_entities(kind,id,payload) VALUES('job',?,?)", (job['id'], json.dumps(job)))
        command = [binary, 'bot', 'start', '--channels', 'weixin', '--dir', str(work), '--heartbeat', '--heartbeat-poll', '1s']
        log = open(Path(tmp) / 'bot.log', 'w+')
        process = None

        def start():
            return subprocess.Popen(command, cwd=work, env=env, stdout=log, stderr=log, start_new_session=True)

        def stop():
            if process and process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM); process.wait(timeout=15)

        def wait_for(predicate, timeout=20):
            until = time.monotonic() + timeout
            while time.monotonic() < until:
                if predicate(): return
                if process.poll() is not None: raise RuntimeError('bot exited')
                time.sleep(.1)
            raise RuntimeError('acceptance timeout')

        def message(text, identity):
            incoming.put(dict(message_id=identity, from_user_id='fixture-owner', to_user_id='fixture', msg_type=1, context_token='fixture-context', item_list=[dict(type=1, text_item=dict(text=text))]))

        try:
            process = start()
            wait_for(lambda: 'DAILY_REPORT' in json.dumps(sent))
            # No inbound message has been enqueued: proactive work created the day.
            with sqlite3.connect(database) as db:
                rows = db.execute('SELECT date,session_id FROM assistant_daily_sessions').fetchall()
            if len(rows) != 1 or rows[0][0] != day: errors.append('proactive first trigger did not create the date-labelled session')
            message('接着刚才的简报说。CASE_FOLLOW', 'follow-1')
            wait_for(lambda: 'FOLLOWUP_OK' in json.dumps(sent))
            message('查一下今天的聊天记录。CASE_HISTORY', 'history-1')
            wait_for(lambda: 'HISTORY_OK' in json.dumps(sent))
            wait_for(lambda: len(requests) >= 5)
            time.sleep(2)
            stop()
            sent_before = len(sent)
            process = start()
            time.sleep(2)
            if len(sent) != sent_before: errors.append('restart resent completed notifications')
            message('继续之前的简报。CASE_RESTART', 'follow-2')
            wait_for(lambda: sum('FOLLOWUP_OK' in json.dumps(m) for m in sent) == 2)
            stop()
            with sqlite3.connect(database) as db:
                rows_after = db.execute('SELECT date,session_id FROM assistant_daily_sessions').fetchall()
                outbox = [json.loads(r[0]) for r in db.execute('SELECT payload FROM assistant_notifications')]
                jobs = [json.loads(r[0]) for r in db.execute("SELECT payload FROM assistant_entities WHERE kind='job'")]
            if rows_after != rows: errors.append('inbound or restart selected a different session')
            titles = [json.loads(p.read_text()) for name in ('catalog-metadata.json', 'recent-v1.json') for p in home.rglob(name)]
            native_title_ok = any(v.get('sessionId') == rows[0][1] and v.get('title') == day for v in titles)
            if not native_title_ok: errors.append('native session list metadata did not retain the YYYY-MM-DD title')
            if len(outbox) != 2 or any(n['state'] != 'delivered' for n in outbox): errors.append('notification outbox not checkpointed')
            if sorted(j['history'][-1]['status'] for j in jobs) != ['completed', 'delivered', 'delivered']: errors.append('job execution/delivery statuses incorrect')
            if any('nothing actionable' in json.dumps(m) or '"notify"' in json.dumps(m) for m in sent): errors.append('silent decision leaked to Weixin')
            result = dict(passed=not errors, proactive_first_created_day=True, inbound_and_restart_reused_day=rows_after == rows, native_history_tool_calls=tool_calls, native_date_title_verified=native_title_ok, notifications=len(outbox), model_requests=len(requests), fake_weixin_messages=len(sent), real_weixin_messages=0, errors=errors)
            print(json.dumps(result, ensure_ascii=False, indent=2))
            if errors: raise RuntimeError('daily acceptance failed')
        except Exception:
            print(json.dumps({"requests":len(requests),"tool_calls":tool_calls,"sent":sent,"last_tools":[m for r in requests[-2:] for m in r.get("messages",[]) if m.get("role")=="tool"]},ensure_ascii=False)[-8000:]); log.flush(); log.seek(0); print(log.read()[-10000:]); raise
        finally:
            stop(); server.shutdown(); log.close()


if __name__ == '__main__':
    main()
