#!/usr/bin/env python3
"""Real CLI/Bot acceptance using synthetic timetable + local Weixin/model endpoints."""
import argparse, datetime as dt, json, os, signal, sqlite3, subprocess, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('binary')
    args = ap.parse_args()
    binary = str(Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix='mybot-course-acceptance-') as tmp:
        home, work = Path(tmp) / 'home', Path(tmp) / 'work'
        home.mkdir(); work.mkdir()
        sent, requests = [], []

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def reply(self, value):
                raw = json.dumps(value).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                try:
                    self.wfile.write(raw)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))) or b'{}')
                if self.path.endswith('/getupdates'):
                    time.sleep(.15)
                    return self.reply({'ret': 0, 'errcode': 0, 'msgs': [], 'get_updates_buf': 'course-fixture'})
                if self.path.endswith('/sendmessage'):
                    sent.append(body)
                    return self.reply({'ret': 0, 'errcode': 0, 'message_id': str(len(sent))})
                if '/ilink/' in self.path:
                    return self.reply({'ret': 0, 'errcode': 0, 'typing_ticket': 'fixture'})
                requests.append(body)
                return self.reply({'error': 'native reminder must not call a model'})

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        base = 'http://127.0.0.1:' + str(server.server_port)
        (home / 'config.toml').write_text(f'''
default_model = "fixture/model"
[permissions]
mode = "ask"
allow = ["assistant_tasks", "assistant_memory", "assistant_courses", "assistant_heartbeat"]
[[providers]]
name = "fixture"
kind = "openai"
base_url = "{base}/v1"
models = ["model"]
default = "model"
api_key_env = "COURSE_FIXTURE_KEY"
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
token_env = "COURSE_FIXTURE_WEIXIN"
api_base = "{base}"
''')
        env = dict(os.environ, REASONIX_HOME=str(home), HOME=str(home), REASONIX_ASSISTANT='1', TZ='Asia/Shanghai', COURSE_FIXTURE_KEY='fixture-key', COURSE_FIXTURE_WEIXIN='fixture-token', DO_NOT_TRACK='1')

        def cli(*a):
            result = subprocess.run([binary, 'assistant', *a], env=env, cwd=work, capture_output=True, text=True, timeout=20)
            if result.returncode:
                raise RuntimeError(result.stderr[-1000:])
            return json.loads(result.stdout)

        zone = dt.timezone(dt.timedelta(hours=8))
        now = dt.datetime.now(zone)
        trigger = (now + dt.timedelta(minutes=1)).replace(second=0, microsecond=0)
        start = trigger + dt.timedelta(minutes=1)
        if start.date() != now.date():
            raise RuntimeError('run acceptance away from civil midnight')
        first = now.date() - dt.timedelta(days=now.weekday())
        config = {'name': 'Synthetic acceptance term', 'first_week_monday': str(first), 'total_weeks': 16, 'timezone': 'Asia/Shanghai', 'periods': [{'number': 1, 'start': start.strftime('%H:%M'), 'end': (start + dt.timedelta(minutes=45)).strftime('%H:%M')}], 'courses': [{'id': 'acceptance', 'title': 'COURSE_ACCEPTANCE', 'weekday': now.isoweekday(), 'periods': [1], 'room': 'ROOM_ACCEPTANCE'}]}
        imported = home / 'import.json'
        imported.write_text(json.dumps({'action': 'import', 'revision': 0, 'config': config}))
        cli('courses-import', str(imported))
        day = cli('courses', json.dumps({'action': 'query', 'date': str(now.date())}))
        assert day['courses'][0]['room'] == 'ROOM_ACCEPTANCE'
        cfg = cli('heartbeat-init')
        week = {d: {'enabled': True, 'start': trigger.strftime('%H:%M'), 'end': '24:00'} for d in ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday']}

        def change(**fields):
            current = cli('heartbeat')['config']
            return cli('heartbeat', json.dumps(dict(revision=current['revision'], **fields)))

        change(action='set_weekly', weekly_schedule=week)
        change(action='set_module_enabled', module_id='course_reminder', enabled=True)
        change(action='set_trigger', trigger_id='courses', mode='course', modules=['course_reminder'], lead_minutes=1, catch_up_seconds=60)
        database = home / 'assistant' / 'state.sqlite3'
        # A pre-window fixed event must be marked skipped, never postponed to wake time.
        early = trigger - dt.timedelta(minutes=1)
        job = {'id': 'outside-window', 'title': 'outside-window', 'prompt': 'MUST_NOT_SEND', 'at': early.isoformat(), 'timezone': 'Asia/Shanghai', 'enabled': True, 'kind': 'message', 'workspaceRoot': str(work), 'approvalMode': 'read-only', 'target': {'connection_id': 'weixin', 'domain': 'weixin', 'chat_id': 'fixture-owner', 'chat_type': 'dm'}}
        with sqlite3.connect(database) as db:
            db.execute("INSERT INTO assistant_entities(kind,id,payload) VALUES('job',?,?)", (job['id'], json.dumps(job)))
        log = open(home / 'bot.log', 'w+')
        command = [binary, 'bot', 'start', '--channels', 'weixin', '--dir', str(work), '--heartbeat', '--heartbeat-poll', '1s']
        process = subprocess.Popen(command, cwd=work, env=env, stdout=log, stderr=log, start_new_session=True)
        try:
            deadline = trigger.timestamp() + 25
            durable = False
            while time.time() < deadline:
                if process.poll() is not None:
                    raise RuntimeError('Bot exited')
                with sqlite3.connect(database, timeout=5) as db:
                    completed = [json.loads(p) for p, in db.execute("SELECT payload FROM assistant_entities WHERE kind='job' AND json_extract(payload,'$.module_id')='course_reminder'")]
                    has_outbox = db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='assistant_notifications'").fetchone()
                    delivered = [json.loads(p) for p, in db.execute('SELECT payload FROM assistant_notifications')] if has_outbox else []
                durable = bool(completed and completed[0].get('history') and completed[0]['history'][-1]['status'] == 'delivered' and delivered and delivered[0]['state'] == 'delivered')
                if durable:
                    break
                time.sleep(.2)
            assert durable, 'notification did not reach durable delivered state'
            assert len(sent) == 1, 'expected exactly one native course message'
            assert 'ROOM_ACCEPTANCE' in json.dumps(sent), 'room omitted'
            assert 'MUST_NOT_SEND' not in json.dumps(sent), 'outside event bypassed global routine'
            assert not requests, 'native course reminder called a model'
            os.killpg(process.pid, signal.SIGTERM); process.wait(timeout=15)
            with sqlite3.connect(database) as db:
                jobs = [json.loads(p) for p, in db.execute("SELECT payload FROM assistant_entities WHERE kind='job'")]
                course = [j for j in jobs if j.get('module_id') == 'course_reminder']
                outside = next(j for j in jobs if j['id'] == 'outside-window')
                notices = [json.loads(p) for p, in db.execute('SELECT payload FROM assistant_notifications')]
            assert len(course) == 1 and course[0]['history'][-1]['status'] == 'delivered'
            assert outside['history'][-1]['status'] == 'skipped' and not outside['enabled']
            assert len(notices) == 1 and notices[0]['state'] == 'delivered'
            process = subprocess.Popen(command, cwd=work, env=env, stdout=log, stderr=log, start_new_session=True)
            time.sleep(3)
            os.killpg(process.pid, signal.SIGTERM); process.wait(timeout=15)
            assert len(sent) == 1 and not requests, 'restart duplicated notification'
            print(json.dumps({'passed': True, 'actual_binary': True, 'timetable_query_has_room': True, 'outside_routine_fixed_event': 'skipped', 'course_notification': 'delivered', 'outbox_and_conversation_import': 'delivered', 'restart_duplicate_count': 0, 'fake_weixin_messages': len(sent), 'model_requests': len(requests), 'real_weixin_messages': 0}, indent=2))
        except Exception:
            log.flush(); log.seek(0)
            print(log.read()[-6000:])
            raise
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM); process.wait(timeout=15)
            server.shutdown(); log.close()


if __name__ == '__main__':
    main()
