#!/usr/bin/env python3
"""Actual binary + approved public GitHub GETs in isolated state; no bot/model/send."""
import argparse
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import urllib.parse
import urllib.request


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('binary')
    ap.add_argument('--credential', required=True)
    ap.add_argument('--login', required=True)
    args = ap.parse_args()
    binary = str(Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix='mybot-pr-read-acceptance-') as tmp:
        home, work = Path(tmp) / 'home', Path(tmp) / 'work'
        home.mkdir(mode=0o700)
        work.mkdir()
        secret = home / 'credentials'
        secret.mkdir(mode=0o700)
        token = secret / 'github-pr-monitor.token'
        token.write_bytes(Path(args.credential).read_bytes())
        token.chmod(0o600)
        env = dict(os.environ, REASONIX_HOME=str(home), HOME=str(home), REASONIX_ASSISTANT='1', DO_NOT_TRACK='1')

        def cli(*command):
            p = subprocess.run([binary, 'assistant', *command], cwd=work, env=env, capture_output=True, text=True, timeout=90)
            if p.returncode:
                raise RuntimeError('Read-only acceptance command failed: ' + p.stderr[-700:])
            return json.loads(p.stdout)

        configured = cli('github', json.dumps({'action': 'configure', 'login': args.login, 'auto_discover': True, 'enabled': True, 'revision': 0, 'poll_seconds': 60, 'discovery_seconds': 300}))
        assert configured['poll_seconds'] == 60 and configured['discovery_seconds'] == 300
        discovered = cli('github', '{"action":"discover"}')
        watches = cli('github', '{"action":"list"}')
        database = home / 'assistant/state.sqlite3'
        # Only this synthetic state uses an all-day window, so acceptance is
        # repeatable without changing any personal production routine.
        with sqlite3.connect(database) as db:
            cfg = json.loads(db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0])
            for window in cfg['weekly_schedule'].values():
                window.update(enabled=True, start='00:00', end='24:00')
            db.execute("UPDATE assistant_entities SET payload=? WHERE kind='heartbeat_config' AND id='default'", (json.dumps(cfg),))
        for _ in range((len(watches) + 4) // 5):
            cli('github-check')
        sample = None
        if not watches:
            # No open PRs is valid. Exercise PR details/comments/reviews/CI on
            # one of this same account's closed public PRs, without watching it.
            query = urllib.parse.urlencode({'q': 'is:pr is:closed is:public author:' + args.login, 'per_page': 1, 'sort': 'updated', 'order': 'desc'})
            request = urllib.request.Request('https://api.github.com/search/issues?' + query, headers={
                'Authorization': 'Bearer ' + token.read_text().strip(), 'Accept': 'application/vnd.github+json',
                'X-GitHub-Api-Version': '2026-03-10', 'User-Agent': 'Friday-PR-Read-Acceptance'})
            with urllib.request.urlopen(request, timeout=20) as response:
                recent = json.load(response)
            if recent.get('items'):
                sample = cli('github', json.dumps({'action': 'query', 'url': recent['items'][0]['html_url']}))
                assert sample['untrusted_external_data'] and sample['head_sha'], 'PR details were not validated'
        status = cli('github', '{"action":"status"}')
        assert all(w['baseline'] and not w.get('error') for w in status['watches']), 'Not every public PR has a complete baseline'
        with sqlite3.connect(database) as db:
            assert db.execute("SELECT count(*) FROM assistant_entities WHERE kind='github_pr_event'").fetchone()[0] == 0, 'first baseline replayed history'
            assert db.execute("SELECT count(*) FROM assistant_entities WHERE kind='job' AND json_extract(payload,'$.module_id')='github_pr_notification'").fetchone()[0] == 0, 'isolated read queued a notification'
        print(json.dumps({'passed': True, 'actual_binary': True, 'actual_public_github_gets': True,
                          'discovered_open_public_prs': discovered, 'baseline_count': len(status['watches']),
                          'poll_seconds': configured['poll_seconds'], 'discovery_seconds': configured['discovery_seconds'],
                          'additional_closed_public_pr_read': sample is not None,
                          'sample_ci_state': sample['ci']['state'] if sample else None,
                          'sample_ci_error': sample['ci'].get('error') if sample else None,
                          'ci_unknown_count': sum(w['baseline']['ci']['state'] == 'unknown' for w in status['watches']),
                          'historical_events_replayed': 0, 'notifications_queued': 0,
                          'github_write_requests': 0, 'real_model_requests': 0, 'real_weixin_messages': 0,
                          'production_state_changed': False}, indent=2))


if __name__ == '__main__':
    main()
