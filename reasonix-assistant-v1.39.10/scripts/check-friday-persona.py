#!/usr/bin/env python3
"""Check real-provider persona replacement in disposable state on the server."""
import json
import os
from pathlib import Path
import pwd
import shutil
import subprocess
import tempfile
import threading
import tomllib
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    production = Path('/var/lib/mybot/reasonix')
    source = Path('/home/ubuntu/Project/MyBot/reasonix/deploy/tencent/friday.md')
    account = pwd.getpwnam('mybot')
    with tempfile.TemporaryDirectory(prefix='friday-persona-check-', dir='/var/lib/mybot') as tmp:
        root = Path(tmp)
        home, work = root / 'home', root / 'work'
        home.mkdir()
        work.mkdir()
        for p in (root, home, work):
            p.chmod(0o700)
            os.chown(p, account.pw_uid, account.pw_gid)
        executable = root / 'reasonix'
        shutil.copyfile(os.environ.get('MYBOT_FRIDAY_BINARY', '/opt/mybot/current/reasonix'), executable)
        executable.chmod(0o700)
        os.chown(executable, account.pw_uid, account.pw_gid)
        for name in ('config.toml', '.env'):
            p = home / name
            shutil.copyfile(production / name, p)
            p.chmod(0o600)
            os.chown(p, account.pw_uid, account.pw_gid)
        config_path = home / 'config.toml'
        original = config_path.read_text()
        if any(k in tomllib.loads(original).get('agent', {}) for k in ('system_prompt', 'system_prompt_file')):
            raise RuntimeError('Expected the pre-persona production configuration')
        upstream = next(p['base_url'].rstrip('/') for p in tomllib.loads(original)['providers'] if p['name'] == 'ali-medium')
        wire_systems = []

        class Proxy(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                data = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                request_body = json.loads(data)
                wire_systems.append('\n'.join(m.get('content', '') for m in request_body['messages'] if m.get('role') == 'system'))
                headers = {k: v for k, v in self.headers.items() if k.lower() not in ('host', 'content-length', 'connection', 'accept-encoding')}
                request = urllib.request.Request(upstream + '/chat/completions', data=data, headers=headers, method='POST')
                with urllib.request.urlopen(request, timeout=180) as response:
                    body = response.read()
                    self.send_response(response.status)
                    self.send_header('Content-Type', response.headers.get('Content-Type', 'application/json'))
                    self.send_header('Content-Length', str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)

        proxy = ThreadingHTTPServer(('127.0.0.1', 0), Proxy)
        threading.Thread(target=proxy.serve_forever, daemon=True).start()
        original = original.replace(upstream, 'http://127.0.0.1:' + str(proxy.server_port) + '/v1')
        config_path.write_text(original)
        base = ['sudo', '-n', '-u', 'mybot', 'env', 'HOME=' + str(home),
                'REASONIX_HOME=' + str(home), 'REASONIX_ASSISTANT=1',
                'TZ=Asia/Shanghai', 'REASONIX_TELEMETRY=off',
                str(executable), 'run', '--output-format', 'json',
                '--permission-mode', 'read-only', '--dir', str(work), '--max-steps', '8']

        def run(prompt, resume=None):
            args = base + (['--resume', resume] if resume else []) + ['--', prompt]
            r = subprocess.run(args, capture_output=True, text=True, cwd=work, timeout=180)
            if r.returncode:
                raise RuntimeError('CLI failed, exit=' + str(r.returncode))
            v = json.loads(r.stdout)
            if v.get('is_error'):
                raise RuntimeError('Model reported an error')
            return v.get('result', '')

        run('你好，仅简短回复一句问候。不要调用工具。')
        manifests = [p for p in home.rglob('manifest.json') if p.parent.parent.name == 'sessions-v4']
        if len(manifests) != 1:
            raise RuntimeError('Expected one disposable conversation')
        session_id = json.loads(manifests[0].read_text())['sessionId']
        p = home / 'friday.md'
        shutil.copyfile(source, p)
        p.chmod(0o600)
        os.chown(p, account.pw_uid, account.pw_gid)
        changed = original.replace('[agent]\n', '[agent]\nsystem_prompt_file = ' + json.dumps(str(p)) + '\n', 1)
        assert tomllib.loads(changed)['agent']['system_prompt_file'] == str(p)
        config_path.write_text(changed)
        answer = run('在吗', session_id)
        leading = wire_systems[-1]
        refreshed = leading.startswith('# Friday 的身份与工作方式') and 'You are Reasonix, a coding agent.' not in leading
        print(json.dumps({'resumed_system_prompt_refreshed': refreshed,
                          'greeting': answer, 'production_state_modified': False}, ensure_ascii=False), flush=True)
        if not refreshed:
            raise RuntimeError('Existing conversation retained its old system prompt')
        if 'review the final diff' in leading:
            raise RuntimeError('Assistant still received the unconditional coding policy')
        if 'Sir' not in answer or any(s in answer for s in ('写代码', '跑测试', '仓库结构', '编程助手')):
            raise RuntimeError('Greeting retained the coding persona')
        run('这是隔离验收。请实际使用 assistant_memory 保存一条 kind=long、subject=验收_偏好、body=喜欢简短中文的记忆；使用 assistant_tasks 创建一个 kind=todo、title=验收_整理日程 的普通待办，不安排提醒。不发送微信消息。两项均成功后简短确认。')
        import sqlite3
        db = sqlite3.connect(home / 'assistant/state.sqlite3')
        todos = [json.loads(row[0]) for row in db.execute("SELECT payload FROM assistant_entities WHERE kind='todo'")]
        jobs = db.execute("SELECT COUNT(*) FROM assistant_entities WHERE kind='job'").fetchone()[0]
        db.close()
        notes = list((home / 'assistant/memory').glob('*.md'))
        assert len(todos) == 1 and todos[0]['title'] == '验收_整理日程' and jobs == 0
        assert len(notes) == 1 and '验收_偏好' in notes[0].read_text()
        print(json.dumps({'passed': True, 'resumed_persona': True,
                          'memory_tool': True, 'todo_tool': True,
                          'notification_jobs': 0, 'production_state_modified': False}), flush=True)
        proxy.shutdown()


if __name__ == '__main__':
    main()
