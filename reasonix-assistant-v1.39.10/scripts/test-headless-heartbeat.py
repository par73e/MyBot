#!/usr/bin/env python3
"""Exercise the built CLI with local model and Bot fixtures, without real accounts."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', default='bin/reasonix')
    binary = str(Path(parser.parse_args().binary).resolve())
    model_calls, deliveries = [], []
    delivered = threading.Event()
    guard = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            if self.path == '/v1/chat/completions':
                with guard:
                    model_calls.append(body)
                assert body['model'] == 'fixture-model', body
                assert self.headers['Authorization'] == 'Bearer local-fixture-key'
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.end_headers()
                chunk = {'choices': [{'delta': {'content': 'Fixture morning report'}, 'finish_reason': None}]}
                self.wfile.write(('data: ' + json.dumps(chunk) + '\n\ndata: [DONE]\n\n').encode())
            elif self.path == '/send':
                assert self.headers['Authorization'] == 'Bearer local-bot-token'
                assert body['connection_id'] == 'fixture-weixin' and body['chat_id'] == 'fixture-owner'
                with guard:
                    deliveries.append(body)
                    count = len(deliveries)
                self.send_response(503 if count == 1 else 200)
                self.end_headers()
                if count >= 2:
                    delivered.set()
            else:
                self.send_error(404)

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix='reasonix-heartbeat-smoke-') as temporary:
            root = Path(temporary)
            home, workspace = root / 'state', root / 'workspace'
            home.mkdir()
            workspace.mkdir()
            env = dict(os.environ)
            env.update(REASONIX_HOME=str(home), REASONIX_TELEMETRY='0', DO_NOT_TRACK='1',
                       REASONIX_LANG='en', HEARTBEAT_FIXTURE_KEY='local-fixture-key',
                       HEARTBEAT_FIXTURE_BOT_TOKEN='local-bot-token')
            (home / '.env').write_text('HEARTBEAT_FIXTURE_KEY=local-fixture-key\n')
            (home / '.env').chmod(0o600)
            port = server.server_address[1]
            (home / 'config.toml').write_text(f'''default_model = "fixture/fixture-model"
[[providers]]
name = "fixture"
kind = "openai"
base_url = "http://127.0.0.1:{port}/v1"
models = ["fixture-model"]
default = "fixture-model"
api_key_env = "HEARTBEAT_FIXTURE_KEY"
[bot.control]
enabled = true
addr = "127.0.0.1:{port}"
token_env = "HEARTBEAT_FIXTURE_BOT_TOKEN"
''')
            def cli(*args):
                run = subprocess.run([binary, 'heartbeat', *args], env=env, cwd=workspace,
                                     capture_output=True, text=True, timeout=60)
                if run.returncode:
                    raise AssertionError(f'{args}: {run.stderr}\n{run.stdout}')
                return json.loads(run.stdout) if run.stdout.strip() else None

            at = (dt.datetime.now(dt.timezone.utc) + dt.timedelta(seconds=2)).isoformat(timespec='seconds')
            task = cli('add', '--title', 'morning report', '--prompt', 'Summarize the fixture morning report.',
                       '--at', at, '--timezone', 'Asia/Shanghai', '--dir', str(workspace),
                       '--connection', 'fixture-weixin', '--chat', 'fixture-owner')
            worker = subprocess.Popen([binary, 'heartbeat', 'start', '--poll', '1s'],
                                      env=env, cwd=workspace, stdout=subprocess.PIPE,
                                      stderr=subprocess.PIPE, text=True)
            try:
                if not delivered.wait(45):
                    raise AssertionError('Timed worker did not deliver the generated report')
            finally:
                worker.terminate()
                stdout, stderr = worker.communicate(timeout=20)
            if worker.returncode != 0:
                raise AssertionError(f'Worker shutdown failed: {stdout}\n{stderr}')
            tasks = cli('list')
            assert tasks[0]['id'] == task['id'] and not tasks[0]['enabled'], tasks
            assert tasks[0]['history'][-1]['status'] == 'delivered', tasks
            assert len(model_calls) == 1, f'Agent reran after send failure: {len(model_calls)}'
            assert len(deliveries) == 2 and all(x['text'] == 'Fixture morning report' for x in deliveries)
            cli('tick')
            assert len(model_calls) == 1 and len(deliveries) == 2, 'Restart repeated completed work'
            # A fixed reminder exercises the zero-model route in the real binary.
            fixed = cli('add', '--title', 'fixed', '--prompt', 'Submit report', '--kind', 'message',
                        '--at', (dt.datetime.now(dt.timezone.utc) + dt.timedelta(hours=1)).isoformat(timespec='seconds'),
                        '--dir', str(workspace))
            cli('run', '--id', fixed['id'])
            assert len(model_calls) == 1, 'Fixed reminder called the model'
            print(json.dumps({'status': 'passed', 'model_requests': len(model_calls),
                              'delivery_attempts': len(deliveries), 'one_shot_after_restart': 'not repeated',
                              'fixed_reminder_model_requests': 0}, ensure_ascii=False))
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


if __name__ == '__main__':
    main()
