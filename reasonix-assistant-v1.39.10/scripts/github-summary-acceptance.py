#!/usr/bin/env python3
"""Real binary, synthetic PR events, mock model/Weixin; no live GitHub or real notifications."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import re
import signal
import sqlite3
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def scenario(binary, mode):
    with tempfile.TemporaryDirectory(prefix='mybot-github-summary-') as tmp:
        home, work = Path(tmp) / 'home', Path(tmp) / 'work'
        home.mkdir()
        work.mkdir()
        sent, requests, errors = [], [], []
        english = 'General review context.\n' * 100 + 'Please add a regression test for empty input.'

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def reply(self, value, stream=False):
                data = value if stream else json.dumps(value).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream' if stream else 'application/json')
                self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                try:
                    self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))) or b'{}')
                if self.path.endswith('/getupdates'):
                    time.sleep(.1)
                    return self.reply({'ret': 0, 'errcode': 0, 'msgs': [], 'get_updates_buf': 'fixture'})
                if self.path.endswith('/sendmessage'):
                    sent.append(body)
                    return self.reply({'ret': 0, 'errcode': 0, 'message_id': str(len(sent))})
                if '/ilink/' in self.path:
                    return self.reply({'ret': 0, 'errcode': 0, 'typing_ticket': 'fixture'})
                requests.append(body)
                tools = {t.get('function', {}).get('name') for t in body.get('tools', [])}
                if tools != {'assistant_github_notification'}:
                    errors.append('unexpected tools: ' + repr(tools))
                messages = body.get('messages', [])
                sources = [m for m in messages if m.get('role') == 'tool' and 'untrusted_external_data' in str(m.get('content', ''))]
                if not sources:
                    delta = {'tool_calls': [{'index': 0, 'id': 'read-notification', 'type': 'function', 'function': {'name': 'assistant_github_notification', 'arguments': '{"action":"snapshot"}'}}]}
                    finish = 'tool_calls'
                else:
                    if 'Please add a regression test for empty input.' not in json.dumps(sources):
                        errors.append('tail of long reply missing')
                    summary = '希望为当前实现补充空输入的回归测试。' if mode == 'valid' else 'Please add a regression test.'
                    value = {'summaries': [{'id': 'fixture-event', 'summary': summary, 'evidence_quote': 'Please add a regression test for empty input.'}]}
                    delta = {'content': json.dumps(value, ensure_ascii=False) if mode != 'bad_json' else 'invalid JSON'}
                    finish = 'stop'
                data = ('data: ' + json.dumps({'choices': [{'delta': delta, 'finish_reason': finish}]}, ensure_ascii=False) + '\n\ndata: [DONE]\n\n').encode()
                self.reply(data, True)

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        base = 'http://127.0.0.1:' + str(server.server_port)
        (home / 'config.toml').write_text(f'''
default_model = "fixture/model"
[permissions]
mode = "ask"
allow = ["assistant_github_notification"]
[[providers]]
name = "fixture"
kind = "openai"
base_url = "{base}/v1"
models = ["model"]
default = "model"
api_key_env = "GH_SUMMARY_FIXTURE_KEY"
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
token_env = "GH_SUMMARY_FIXTURE_WEIXIN"
api_base = "{base}"
''')
        env = dict(os.environ, REASONIX_HOME=str(home), HOME=str(home), REASONIX_ASSISTANT='1', TZ='Asia/Shanghai', GH_SUMMARY_FIXTURE_KEY='fixture-key', GH_SUMMARY_FIXTURE_WEIXIN='fixture-token', DO_NOT_TRACK='1')
        init = subprocess.run([binary, 'assistant', 'heartbeat-init'], cwd=work, env=env, capture_output=True, text=True, timeout=20)
        assert init.returncode == 0, init.stderr
        cfg = json.loads(init.stdout)
        cfg['weekly_schedule'] = {d: {'enabled': True, 'start': '00:00', 'end': '24:00'} for d in ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday']}
        cfg['date_overrides'] = {}
        cfg['triggers'] = {'github_prs': {'id': 'github_prs', 'title': 'fixture', 'enabled': True, 'mode': 'interval', 'interval_seconds': 60, 'routine_bound': True, 'modules': ['github_pr_check']}}
        cfg['module_enabled'] = {k: False for k in cfg['module_enabled']}
        cfg['module_enabled']['github_pr_check'] = True
        now = dt.datetime.now(dt.timezone(dt.timedelta(hours=8)))
        stamp = lambda x: x.isoformat(timespec='seconds')
        pr = 'https://github.com/org/project/pull/1'
        item = {'id': 'comment:1', 'kind': 'comment', 'author': 'reviewer', 'text': 'preview only', 'fingerprint': 'fixture-fingerprint', 'url': pr + '#issuecomment-1'}
        snap = {'url': pr, 'repository': 'org/project', 'number': 1, 'state': 'open', 'head_sha': 'a' * 40, 'items': {'comment:1': item}, 'ci': {'state': 'success'}}
        watch = {'id': 'fixture-watch', 'revision': 1, 'url': pr, 'repository': 'org/project', 'number': 1, 'enabled': True, 'next_check_at': stamp(now + dt.timedelta(hours=1)), 'baseline': snap}
        event = {'id': 'fixture-event', 'watch_id': watch['id'], 'source_id': 'comment:1', 'fingerprint': item['fingerprint'], 'kind': 'comment', 'author': 'reviewer', 'label': '有新回复', 'body': english, 'url': item['url'], 'job_id': 'fixture-notify'}
        source = Path(__file__).resolve().parents[1] / 'internal/assistant/github_summary.go'
        prompt = re.search(r'const GitHubSummaryInstructions = `(.*?)`', source.read_text(), re.S)[1]
        job = {'id': 'fixture-notify', 'title': 'GitHub PR进展', 'prompt': prompt, 'at': stamp(now - dt.timedelta(seconds=1)), 'endsAt': stamp(now + dt.timedelta(minutes=3)), 'timezone': 'Asia/Shanghai', 'kind': 'agent', 'capability': 'github', 'decide_notification': True, 'newConversationEachRun': True, 'approvalMode': 'read-only', 'enabled': True, 'workspaceRoot': str(work), 'model': 'fixture/model', 'module_id': 'github_pr_notification', 'github_watch_id': watch['id'], 'github_event_ids': [event['id']], 'target': {'connection_id': 'weixin', 'domain': 'weixin', 'chat_id': 'fixture-owner', 'chat_type': 'dm'}}
        database = home / 'assistant/state.sqlite3'
        with sqlite3.connect(database) as db:
            for kind, ident, value in [('heartbeat_config', 'default', cfg), ('github_config', 'default', {'revision': 1, 'enabled': True, 'login': 'owner', 'auto_discover': False, 'poll_seconds': 60, 'discovery_seconds': 300}), ('github_pr_watch', watch['id'], watch), ('github_pr_event', event['id'], event), ('job', job['id'], job)]:
                db.execute('INSERT OR REPLACE INTO assistant_entities(kind,id,payload) VALUES(?,?,?)', (kind, ident, json.dumps(value)))
        command = [binary, 'bot', 'start', '--channels', 'weixin', '--dir', str(work), '--heartbeat', '--heartbeat-poll', '1s']
        log = open(home / 'bot.log', 'w+')
        process = subprocess.Popen(command, cwd=work, env=env, stdout=log, stderr=log, start_new_session=True)
        try:
            deadline = time.time() + 50
            delivered = False
            while time.time() < deadline:
                with sqlite3.connect(database) as db:
                    saved = json.loads(db.execute("SELECT payload FROM assistant_entities WHERE kind='job' AND id='fixture-notify'").fetchone()[0])
                    delivered = bool(saved.get('history') and saved['history'][-1]['status'] == 'delivered')
                if delivered:
                    break
                if process.poll() is not None:
                    raise RuntimeError('bot exited')
                time.sleep(.1)
            assert delivered and len(sent) == 1, 'expected exactly one delivered notification'
            rendered = json.dumps(sent, ensure_ascii=False)
            assert '#issuecomment-1' in rendered and 'reviewer' in rendered
            assert 'General review context' not in rendered and 'Please add a regression test' not in rendered
            assert ('补充空输入' if mode == 'valid' else '中文摘要暂不可用') in rendered
            with sqlite3.connect(database) as db:
                saved_event = json.loads(db.execute("SELECT payload FROM assistant_entities WHERE kind='github_pr_event' AND id='fixture-event'").fetchone()[0])
                assert saved_event.get('delivered_at'), 'delivery not acknowledged'
            os.killpg(process.pid, signal.SIGTERM)
            process.wait(timeout=15)
            request_count = len(requests)
            process = subprocess.Popen(command, cwd=work, env=env, stdout=log, stderr=log, start_new_session=True)
            time.sleep(2)
            os.killpg(process.pid, signal.SIGTERM)
            process.wait(timeout=15)
            assert len(sent) == 1 and len(requests) == request_count, 'restart regenerated or resent'
            assert not errors, errors
            return {'mode': mode, 'passed': True, 'mock_model_requests': len(requests), 'mock_weixin_messages': len(sent), 'restart_duplicates': 0}
        except Exception:
            log.flush()
            log.seek(0)
            print(log.read()[-6000:])
            raise
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
                process.wait(timeout=15)
            server.shutdown()
            log.close()


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('binary')
    args = parser.parse_args()
    results = [scenario(str(Path(args.binary).resolve()), mode) for mode in ['valid', 'english_only', 'bad_json']]
    print(json.dumps({'passed': True, 'actual_binary': True, 'synthetic_events': True, 'real_model_requests': 0, 'real_github_requests': 0, 'real_weixin_messages': 0, 'scenarios': results}, indent=2))
