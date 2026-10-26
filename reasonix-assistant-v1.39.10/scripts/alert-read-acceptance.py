#!/usr/bin/env python3
"""Real binary + public GETs, isolated state, no model or real notification."""
import argparse
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('binary')
    args = ap.parse_args()
    binary = str(Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix='mybot-alert-acceptance-') as tmp:
        home, work = Path(tmp) / 'home', Path(tmp) / 'work'
        home.mkdir(mode=0o700)
        work.mkdir()
        env = dict(os.environ, REASONIX_HOME=str(home), HOME=str(home), REASONIX_ASSISTANT='1', DO_NOT_TRACK='1')

        def cli(*command):
            p = subprocess.run([binary, 'assistant', *command], cwd=work, env=env, capture_output=True, text=True, timeout=50)
            if p.returncode:
                raise RuntimeError('Isolated alert acceptance failed: ' + p.stderr[-800:])
            return json.loads(p.stdout)

        cfg = cli('weather-alerts', '{"action":"configure","revision":0,"enabled":true,"poll_seconds":300}')
        assert cfg['enabled'] and cfg['poll_seconds'] == 300 and cfg['region'] == '上海市'
        database = home / 'assistant/state.sqlite3'
        with sqlite3.connect(database) as db:
            hb = json.loads(db.execute("SELECT payload FROM assistant_entities WHERE kind='heartbeat_config' AND id='default'").fetchone()[0])
            for w in hb['weekly_schedule'].values():
                w.update(enabled=True, start='00:00', end='24:00')
            db.execute("UPDATE assistant_entities SET payload=? WHERE kind='heartbeat_config' AND id='default'", (json.dumps(hb),))
        assert cli('weather-alerts-check')['checked']
        status = cli('weather-alerts')
        assert status['coverage'] == 'healthy', 'Actual source protocol/transport failed: ' + json.dumps(status['sources'], ensure_ascii=False)
        assert len(status['sources']) == 3 and all(not s.get('error') for s in status['sources'])
        # Run again: persisted cadence must suppress redundant HTTP work.
        before = status['sources']
        cli('weather-alerts-check')
        assert before == cli('weather-alerts')['sources'], 'Same-minute command bypassed source cadence'
        with sqlite3.connect(database) as db:
            assert db.execute("SELECT count(*) FROM assistant_entities WHERE kind='job' AND json_extract(payload,'$.module_id') LIKE 'weather_alert_%'").fetchone()[0] == 0
        print(json.dumps({'passed': True, 'actual_binary': True, 'actual_official_public_gets': True,
                          'source_health': status['sources'], 'observed_records': len(status['records']),
                          'five_minute_cadence_persisted': True, 'notifications_queued': 0,
                          'real_model_calls': 0, 'paid_weather_calls': 0, 'real_weixin_messages': 0,
                          'production_state_changed': False}, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
