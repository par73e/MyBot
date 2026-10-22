#!/usr/bin/env python3
"""Verify actual Reasonix effort fields at the HTTP boundary and call upstream.

Run as the service account. Keys stay in server-private temporary files and are
never included in the report. Each preset makes one short real model request.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import time
import tomllib
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    state = Path('/var/lib/mybot/reasonix')
    config = tomllib.loads((state / 'config.toml').read_text())
    expected = {'ali': 'low', 'ali-medium': 'medium', 'ali-xhigh': 'xhigh'}
    entries = [p for p in config['providers'] if p['name'] in expected]
    assert len(entries) == 3
    base = entries[0]['base_url']
    assert all(p['base_url'] == base for p in entries)
    key_line = next(s for s in (state / '.env').read_text().splitlines()
                    if s.startswith('QIANWEN_API_KEY='))
    observed = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            data = self.rfile.read(int(self.headers['Content-Length']))
            body = json.loads(data)
            observed.append({k: body.get(k) for k in
                             ['model', 'reasoning_effort', 'thinking_budget', 'thinking', 'enable_thinking']})
            request = urllib.request.Request(base + '/chat/completions', data=data, headers={
                'Authorization': self.headers['Authorization'], 'Content-Type': 'application/json'})
            try:
                with urllib.request.urlopen(request, timeout=90) as response:
                    self.send_response(response.status)
                    self.send_header('Content-Type', response.headers.get('Content-Type', 'text/event-stream'))
                    self.end_headers()
                    shutil.copyfileobj(response, self.wfile)
            except (urllib.error.URLError, TimeoutError):
                self.send_error(502, 'Upstream request failed; diagnostic body suppressed')

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    results = []
    try:
        with tempfile.TemporaryDirectory(prefix='mybot-effort-verification-') as scratch:
            root = Path(scratch)
            home, workspace = root / 'state', root / 'workspace'
            home.mkdir(mode=0o700)
            workspace.mkdir(mode=0o700)
            (home / '.env').write_text(key_line + '\n')
            (home / '.env').chmod(0o600)
            chunks = ['default_model = "ali/qwen3.8-flash"\n']
            for entry in entries:
                chunks.append('\n[[providers]]\n')
                for field in ['name', 'kind', 'base_url', 'api_key_env', 'models', 'default',
                              'reasoning_protocol', 'supported_efforts', 'default_effort', 'effort']:
                    chunks.append(field + ' = ' + json.dumps(entry[field]) + '\n')
                chunks.append('request_url = "http://127.0.0.1:' + str(server.server_port) + '/chat/completions"\n')
            (home / 'config.toml').write_text(''.join(chunks))
            env = dict(os.environ, REASONIX_HOME=str(home), REASONIX_LANG='zh', DO_NOT_TRACK='1',
                       REASONIX_TELEMETRY='0', TZ='Asia/Shanghai')
            for name, effort in expected.items():
                before = len(observed)
                started = time.monotonic()
                run = subprocess.run([
                    '/opt/mybot/current/reasonix', 'run', '--output-format', 'json',
                    '--permission-mode', 'read-only', '--model', name + '/qwen3.8-flash',
                    '--dir', str(workspace), '--', '这是参数连接测试，请只回复 OK，不要调用工具。'],
                    env=env, cwd=workspace, capture_output=True, text=True, timeout=120)
                assert run.returncode == 0, 'Reasonix execution failed; secret-bearing diagnostics suppressed'
                result = json.loads(run.stdout)
                assert result['type'] == 'result' and not result.get('is_error') and result.get('result')
                assert len(observed) > before, 'No HTTP request observed'
                calls = observed[before:]
                assert all(c['model'] == 'qwen3.8-flash' and c['reasoning_effort'] == effort for c in calls), calls
                assert all(c['thinking_budget'] is None and c['thinking'] is None for c in calls), calls
                assert all(c['enable_thinking'] is not False for c in calls), calls
                results.append({'preset': name, 'wire_effort': effort, 'upstream': 'passed',
                                'requests': len(calls), 'seconds': round(time.monotonic() - started, 2)})
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)
    print(json.dumps({'status': 'passed', 'presets': results}, ensure_ascii=False))


if __name__ == '__main__':
    main()
