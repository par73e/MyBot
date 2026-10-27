#!/usr/bin/env python3
"""Exercise the actual CLI/controller/tool boundary with a local mock model."""
import argparse
import json
import os
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('binary')
    binary = str(Path(parser.parse_args().binary).resolve())
    cases = [
        ('preference', '以后回答先给结论', 'preference', 'long', '以后回答先给结论', '以后回答先给结论', True),
        ('plan', '2099年1月1日至5日在杭州', 'temporary_plan', 'short', '2099年1月1日至5日在杭州', '2099年1月1日至5日在杭州', True),
        ('event', '这条就是我自己配置的。是在帮你做配置呀', 'personal_fact', 'long', '这条就是我自己配置的', '这条就是我自己配置的', False),
        ('expansion', '这是我自己配置的', 'personal_fact', 'long', '这是我自己配置的', '这是我自己配置的，以后相关安全邮件不再提醒', False),
        ('invented_source', '知道了', 'preference', 'long', '以后相关安全邮件不再提醒', '以后相关安全邮件不再提醒', False),
    ]
    results = []
    for name, prompt, category, kind, quote, body, accepted in cases:
        with tempfile.TemporaryDirectory(prefix='mybot-memory-source-') as temp:
            home, work = Path(temp) / 'home', Path(temp) / 'work'
            home.mkdir()
            work.mkdir()
            requests = []
            request = {'action': 'create', 'subject': '测试主题', 'kind': kind, 'category': category,
                       'body': body, 'source_quote': quote}
            if kind == 'short':
                request.update(starts_at='2099-01-01T00:00:00+08:00', expires_at='2099-01-06T00:00:00+08:00')

            class Handler(BaseHTTPRequestHandler):
                def log_message(self, *args):
                    pass

                def do_POST(self):
                    payload = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                    requests.append(payload)
                    if len(requests) == 1:
                        delta = {'tool_calls': [{'index': 0, 'id': 'memory-source', 'type': 'function',
                                 'function': {'name': 'assistant_memory', 'arguments': json.dumps(request)}}]}
                        finish = 'tool_calls'
                    else:
                        delta, finish = {'content': 'MEMORY_CASE_DONE'}, 'stop'
                    data = ('data: ' + json.dumps({'choices': [{'delta': delta, 'finish_reason': finish}]})
                            + '\n\ndata: [DONE]\n\n').encode()
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/event-stream')
                    self.send_header('Content-Length', str(len(data)))
                    self.end_headers()
                    self.wfile.write(data)

            server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            (home / 'config.toml').write_text(f'''
default_model = "fixture/model"
[permissions]
mode = "ask"
allow = ["assistant_memory"]
[[providers]]
name = "fixture"
kind = "openai"
base_url = "http://127.0.0.1:{server.server_port}/v1"
models = ["model"]
default = "model"
api_key_env = "MEMORY_SOURCE_FIXTURE_KEY"
''')
            env = dict(os.environ, HOME=str(home), REASONIX_HOME=str(home), REASONIX_ASSISTANT='1',
                       TZ='Asia/Shanghai', MEMORY_SOURCE_FIXTURE_KEY='fixture', REASONIX_TELEMETRY='off')
            try:
                run = subprocess.run([binary, 'run', '--output-format', 'json', '--permission-mode',
                                      'workspace-write', '--dir', str(work), '--max-steps', '5', '--', prompt],
                                     cwd=work, env=env, text=True, capture_output=True, timeout=30)
                assert run.returncode == 0, (name, run.stderr[-1000:], run.stdout[-1000:])
                files = list((home / 'assistant' / 'memory').glob('*.md'))
                responses = [str(m.get('content', '')) for req in requests[1:]
                             for m in req.get('messages', []) if m.get('role') == 'tool']
                assert len(files) == int(accepted), (name, len(files), responses)
                assert responses, (name, 'no observed tool response')
                if accepted:
                    note = files[0].read_text()
                    metadata = json.loads(note.split('---', 2)[1])
                    assert metadata['source_message_id'] and metadata['category'] == category
                    assert note.split('\n---\n', 1)[1].strip() == body
                else:
                    assert any('memory not saved' in value for value in responses), (name, responses)
                results.append({'case': name, 'saved': accepted, 'mock_model_requests': len(requests)})
            finally:
                server.shutdown()
                server.server_close()
    print(json.dumps({'passed': True, 'actual_cli_controller': True, 'cases': results,
                      'real_model_requests': 0, 'real_weixin_messages': 0}, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
