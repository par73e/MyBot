import datetime as dt
import importlib.util
import json
from pathlib import Path

spec = importlib.util.spec_from_file_location('activity', Path(__file__).parents[1] / 'deploy/health/server_activity_snapshot.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
NOW = dt.datetime(2026, 11, 5, 15, tzinfo=dt.timezone(dt.timedelta(hours=8)))

def test_access_window_rotation_and_privacy(tmp_path):
    old, new = tmp_path/'access.log.1', tmp_path/'access.log'
    old.write_text('1.2.3.4 - - [04/Nov/2026:14:59:00 +0800] "GET / HTTP/1.1" 200 1 "-" "-"\n'
                   '1.2.3.4 - - [04/Nov/2026:15:01:00 +0800] "GET /token?secret=abc HTTP/1.1" 200 10 "-" "-"\n')
    new.write_text('1.2.3.4 - - [05/Nov/2026:14:00:00 +0800] "GET / HTTP/1.1" 404 2 "-" "-"\n'
                   '2.3.4.5 - - [05/Nov/2026:14:00:00 +0800] "GET / HTTP/1.1" 503 - "-" "-"\nbad\n')
    result = m.log_counts([old, new], NOW, True)
    assert result['requests'] == 3
    assert result['unique_source_ips'] == 2
    assert result['status_classes'] == {'2xx': 1, '4xx': 1, '5xx': 1}
    assert result['response_bytes'] == 12
    assert result['invalid_lines'] == 1
    assert 'secret' not in json.dumps(result) and '1.2.3.4' not in json.dumps(result)

def test_fail2ban_counts_are_events_not_attackers(tmp_path):
    log = tmp_path/'fail2ban.log'
    log.write_text('2026-11-05 14:00:00,123 fail2ban.actions [1]: NOTICE [sshd] Ban 1.2.3.4\n'
                   '2026-11-05 14:01:00,123 fail2ban.actions [1]: NOTICE [sshd] Unban 1.2.3.4\n')
    result = m.log_counts([log], NOW)
    assert result['events'] == {'ban': 1, 'unban': 1}
    assert '1.2.3.4' not in json.dumps(result)

def test_bounded_tail_marks_partial_coverage(tmp_path, monkeypatch):
    log = tmp_path/'access.log'
    log.write_text('discard\n' * 20 + '2.3.4.5 - - [05/Nov/2026:14:00:00 +0800] "GET / HTTP/1.1" 200 1 "-" "-"\n')
    monkeypatch.setattr(m, 'MAX_BYTES', 100)
    assert m.log_counts([log], NOW, True)['truncated']
