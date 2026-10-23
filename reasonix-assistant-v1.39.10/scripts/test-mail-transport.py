#!/usr/bin/env python3
"""Offline protocol regression tests; never connects to personal accounts."""
import base64
import importlib.util
import json
from pathlib import Path
import unittest
import tempfile
import time
import io
import os
from unittest.mock import patch
import urllib.error

path = Path(__file__).resolve().parents[1] / 'internal/assistant/mail_transport.py'
spec = importlib.util.spec_from_file_location('mail_transport', path)
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class FakeIMAP:
    def __init__(self): self.sock = self; self.validity = 8; self.ids = [10, 11]; self.fetches = []; self.selections = []
    def settimeout(self, value): pass
    def select(self, folder, readonly=False):
        self.selections.append((folder, readonly)); return 'OK', [b'2']
    def response(self, name): return name, [str(self.validity).encode()]
    def uid(self, operation, *args):
        if operation == 'search': return 'OK', [b' '.join(str(v).encode() for v in self.ids)]
        self.fetches.append(args)
        if 'HEADER.FIELDS' in args[1]:
            return 'OK', [(b'1 (UID 11 INTERNALDATE "23-Oct-2026 22:09:00 +0800"', b'From: sender@example.com\r\nTo: owner@outlook.com\r\nSubject: hello\r\nMessage-ID: <test>\r\nDate: Fri, 23 Oct 2026 22:09:00 +0800\r\n\r\n')]
        return 'OK', [(b'1 (UID 11 RFC822.SIZE 100', b'Content-Type: text/plain; charset=utf-8\r\n\r\nhello')]


class Tests(unittest.TestCase):
    def qq(self):
        q = m.QQ.__new__(m.QQ); q.account = {'folders': ['INBOX']};q.deadline = time.monotonic()+100;q.partial=False; q.c = FakeIMAP(); return q

    def test_qq_baseline_increment_and_read_never_set_seen(self):
        q = self.qq()
        messages, cursor, warnings = q.sync(None)
        self.assertEqual(messages, []); self.assertEqual(q.c.fetches, [])
        q.c.ids.append(12)
        messages, new, _ = q.sync(cursor)
        self.assertEqual([v['transport_id'] for v in messages], ['INBOX:8:12'])
        self.assertEqual(new['INBOX']['uid'], 12)
        self.assertEqual(q.sync(new)[0], [])
        self.assertEqual(q.read('INBOX:8:12')['body'], 'hello')
        self.assertTrue(all(readonly for _, readonly in q.c.selections))
        self.assertTrue(all('BODY.PEEK[' in args[1] for args in q.c.fetches))
        self.assertNotEqual(q.c.fetches[0][1], '(BODY[])')

    def test_qq_identity_reset_is_reconciled_not_skipped(self):
        q = self.qq(); q.c.validity = 9
        messages, cursor, warnings = q.sync({'INBOX': {'validity': 8, 'uid': 100}})
        self.assertEqual(len(messages), 2); self.assertTrue(warnings)
        self.assertEqual(cursor['INBOX'], {'validity': 9, 'uid': 11, 'baseline_at': 0})
        with self.assertRaises(ValueError): q.read('INBOX:8:11')
        with self.assertRaises(ValueError): q.read('Sent:9:11')

    def test_qq_backlog_bounded_and_continues(self):
        q = self.qq();q.c.ids = list(range(1, 151))
        a, cursor, _ = q.sync({'INBOX': {'validity': 8, 'uid': 0}})
        self.assertEqual(len(a), 100)
        b, cursor, _ = q.sync(cursor)
        self.assertEqual(len(b), 50); self.assertEqual(cursor['INBOX']['uid'], 150)

    def test_unicode_mime_html_and_attachment_names(self):
        raw = 'From: Person <person@example.com>\r\nSubject: =?utf-8?b?5rWL6K+V?=\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<style>secret</style><p>你好 &amp; Sir</p><script>bad()</script>'.encode()
        self.assertEqual(m.summary(raw, '1')['subject'], '测试')
        body = m.content(raw)['body']
        self.assertIn('你好 & Sir', body);self.assertNotIn('bad()', body);self.assertNotIn('secret', body)

    def gmail(self):
        g=m.Gmail.__new__(m.Gmail);g.deadline=time.monotonic()+100;g.partial=False;return g

    def test_gmail_history_pages_and_baseline(self):
        g = self.gmail(); requests = []
        def get(path, **params):
            requests.append((path, params))
            if path == 'profile': return {'historyId': '100'}
            if params.get('pageToken') == 'next': return {'historyId': '105', 'history': []}
            return {'historyId': '103', 'history': [{'messagesAdded': [{'message': {'id': 'a'}}, {'message': {'id': 'a'}}]}], 'nextPageToken': 'next'}
        g.get = get;g.metadata = lambda mid: {'transport_id': mid, 'received_at': 200}
        with patch.object(m.time, 'time', return_value=100):
            self.assertEqual(g.sync(None)[0], [])
        out, cursor, _ = g.sync({'history': '100', 'since': 100, 'baseline_since': 100})
        self.assertEqual(len(out), 1); self.assertEqual(cursor['history'], '100');self.assertEqual(cursor['page'], 'next')
        out, end, _ = g.sync(cursor)
        self.assertEqual(out, []); self.assertEqual(end['history'], '105');self.assertNotIn('page', end)

    def test_gmail_expired_history_recovery_pages_preserve_baseline(self):
        g = self.gmail()
        def get(path, **params):
            if path == 'history': raise urllib.error.HTTPError('https://fixture', 404, 'expired', {}, None)
            if path == 'profile': return {'historyId': '500'}
            if params.get('pageToken'): return {'messages': [{'id': 'new'}]}
            return {'messages': [{'id': 'old'}], 'nextPageToken': 'recover-next'}
        g.get = get;g.metadata = lambda mid: {'transport_id': mid, 'received_at': 10 if mid == 'old' else 300}
        out, cursor, warnings = g.sync({'history': '1', 'since': 200, 'baseline_since': 100})
        self.assertEqual(out, []);self.assertTrue(warnings);self.assertTrue(cursor['recovery'])
        out, end, _ = g.sync(cursor)
        self.assertEqual(out[0]['transport_id'], 'new');self.assertEqual(end['history'], '500')

    def test_gmail_full_body_skips_attachment_download(self):
        g = self.gmail(); calls = []
        def get(path, **params):
            calls.append(params)
            return {'payload': {'mimeType': 'multipart/mixed', 'parts': [
                {'mimeType': 'text/plain', 'body': {'data': base64.urlsafe_b64encode('正文'.encode()).decode()}},
                {'mimeType': 'application/pdf', 'filename': 'file.pdf', 'body': {'attachmentId': 'large'}}]}}
        g.get = get
        result = g.read('a');self.assertEqual(result['body'], '正文');self.assertEqual(result['attachments'], ['file.pdf'])
        self.assertEqual(calls, [{'format': 'full'}])

    def test_qq_identity_reset_does_not_notify_prebaseline_history(self):
        q = self.qq(); q.c.validity = 9
        messages, cursor, _ = q.sync({'INBOX': {'validity': 8, 'uid': 100, 'baseline_at': 9999999999}})
        self.assertEqual(messages, []); self.assertEqual(cursor['INBOX']['uid'], 11)

    def test_gmail_refresh_is_cached_and_keeps_refresh_credential_private(self):
        class Opener:
            def __init__(self): self.calls = 0
            def open(self, request, timeout):
                self.calls += 1
                return io.BytesIO(json.dumps({'access_token': 'fresh-fixture', 'expires_in': 3600}).encode())
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp) / 'credential.json'
            p.write_text(json.dumps({'account': 'owner@gmail.com', 'client_id': 'fixture', 'client_secret': 'fixture-secret', 'token': {'access_token': 'expired', 'refresh_token': 'fixture-refresh', 'expires_at': 0}}))
            p.chmod(0o600)
            opener = Opener()
            with patch.object(m.urllib.request, 'build_opener', return_value=opener):
                a = {'address': 'owner@gmail.com', 'credential_file': str(p)}
                self.assertEqual(m.Gmail(a).token, 'fresh-fixture')
                self.assertEqual(m.Gmail(a).token, 'fresh-fixture')
            self.assertEqual(opener.calls, 1)
            self.assertEqual(json.loads(p.read_text())['token']['refresh_token'], 'fixture-refresh')
            self.assertEqual(p.stat().st_mode & 0o777, 0o600)

    def test_gmail_budget_keeps_unread_history_page_ids_for_next_check(self):
        g=self.gmail()
        g.get=lambda path,**params: {'historyId':'200','history':[{'messagesAdded':[{'message':{'id':v}} for v in ['a','b','c']]}]}
        def metadata(mid):
            g.deadline=time.monotonic()-1
            return {'transport_id':mid,'received_at':200}
        g.metadata=metadata
        out,cursor,_=g.sync({'history':'100','since':100,'baseline_since':100})
        self.assertEqual([v['transport_id'] for v in out],['a'])
        self.assertEqual(cursor['pending'],['b','c'])
        g.deadline=time.monotonic()+100
        g.metadata=lambda mid:{'transport_id':mid,'received_at':200}
        g.get=lambda *a,**kw:self.fail('pending IDs must not refetch history')
        out,end,_=g.sync(cursor)
        self.assertEqual([v['transport_id'] for v in out],['b','c']);self.assertEqual(end['history'],'200')

    def test_qq_budget_retains_unprocessed_uids(self):
        q=self.qq();original=q.metadata
        def metadata(*args):
            q.deadline=time.monotonic()-1
            return original(*args)
        def slow_metadata(*args):
            result=original(*args);q.deadline=time.monotonic()-1;return result
        q.metadata=slow_metadata
        out,cursor,warnings=q.sync({'INBOX':{'validity':8,'uid':0}})
        self.assertEqual(len(out),1);self.assertEqual(cursor['INBOX']['uid'],10);self.assertTrue(warnings)
        q.deadline=time.monotonic()+100;q.metadata=original
        out,end,_=q.sync(cursor)
        self.assertEqual(len(out),1);self.assertEqual(end['INBOX']['uid'],11)

    def test_errors_never_return_secret_server_text(self):
        with patch.object(m, 'QQ', side_effect=m.imaplib.IMAP4.error('SECRET-AUTH-CODE')):
            out = m.account_request({'id': 'qq', 'provider': 'qq'}, {'action': 'sync'})
        self.assertEqual(out['error'], 'authentication_failed');self.assertNotIn('SECRET', json.dumps(out))


if __name__ == '__main__': unittest.main()
