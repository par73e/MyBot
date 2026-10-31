"""Private stdlib mail transport embedded by Go. JSON stdin/stdout; no shell or model credentials."""
import base64
import concurrent.futures
import contextlib
import datetime
import email
import email.policy
import fcntl
import hashlib
import html
import http.client
import imaplib
import json
import os
from pathlib import Path
import re
import socket
import ssl
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

MAX_RAW = 2 * 1024 * 1024
MAX_TEXT = 128000
ACCOUNT_SECONDS = 40


class UnixProxyHTTPSConnection(http.client.HTTPSConnection):
    """HTTPS CONNECT over an instance-scoped Unix socket."""
    def __init__(self, host, *, proxy_socket, **kwargs):
        super().__init__(host, **kwargs)
        self.proxy_socket = proxy_socket

    def connect(self):
        if not self._tunnel_host:
            raise OSError('mail proxy tunnel is required')
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            if self.timeout is not socket._GLOBAL_DEFAULT_TIMEOUT:
                self.sock.settimeout(self.timeout)
            self.sock.connect(self.proxy_socket)
            self._tunnel()
            self.sock = self._context.wrap_socket(self.sock, server_hostname=self._tunnel_host)
        except BaseException:
            self.sock.close()
            self.sock = None
            raise


class UnixProxyHTTPSHandler(urllib.request.HTTPSHandler):
    def __init__(self, proxy_socket):
        super().__init__()
        self.proxy_socket = proxy_socket

    def https_open(self, request):
        return self.do_open(lambda host, **kw: UnixProxyHTTPSConnection(host, proxy_socket=self.proxy_socket, **kw),
                            request, context=self._context)


class FixedUnixProxyHandler(urllib.request.BaseHandler):
    def https_request(self, request):
        request.set_proxy('asrobot-mail-proxy.invalid:80', 'http')
        return request


def gmail_opener(account):
    proxy = account.get('proxy') or ''
    if os.environ.get('ASROBOT_INSTANCE_MODE') == 'isolated' and proxy:
        parsed = urllib.parse.urlsplit(proxy)
        if parsed.hostname in ('127.0.0.1', 'localhost', '::1'):
            socket_path = os.environ.get('ASROBOT_MAIL_PROXY_SOCKET', '')
            state = os.environ.get('ASROBOT_RUNTIME_STATE', '')
            root = os.environ.get('ASROBOT_RUNTIME_ROOT', '')
            expected_port = os.environ.get('ASROBOT_MAIL_PROXY_PORT', '')
            if (parsed.scheme != 'http' or parsed.username or parsed.password or parsed.path not in ('', '/') or
                    parsed.query or parsed.fragment or not expected_port.isdigit() or parsed.port != int(expected_port) or
                    not os.path.isabs(root) or os.path.normpath(root) != root or state != os.path.join(root, 'state') or
                    socket_path != os.path.join(state, 'mail-proxy.sock')):
                raise ValueError('isolated mail proxy requires a configured private relay')
            return urllib.request.build_opener(
                urllib.request.ProxyHandler({}), FixedUnixProxyHandler(),
                UnixProxyHTTPSHandler(socket_path))
    return urllib.request.build_opener(urllib.request.ProxyHandler({'https': proxy} if proxy else {}))


class BudgetExceeded(TimeoutError):
    pass


class ContinuePage(Exception):
    pass


def remaining(deadline):
    value = deadline - time.monotonic()
    if value <= 0: raise BudgetExceeded()
    return min(15, value)


def checked(result):
    if result[0] != 'OK':
        raise RuntimeError('mail server rejected operation')
    return result[1]


def utc_date(value):
    try:
        d = email.utils.parsedate_to_datetime(value)
        if not d.tzinfo:
            d = d.replace(tzinfo=datetime.timezone.utc)
        return int(d.timestamp())
    except (ValueError, TypeError, OverflowError):
        return 0


def summary(raw, transport_id, received=0):
    m = email.message_from_bytes(raw, policy=email.policy.default)
    return {'transport_id': str(transport_id), 'message_id': str(m.get('Message-ID', ''))[:1000],
            'from': str(m.get('From', ''))[:2000], 'to': str(m.get('To', ''))[:2000],
            'subject': str(m.get('Subject', ''))[:2000], 'date': str(m.get('Date', ''))[:100],
            'received_at': received or utc_date(str(m.get('Date', ''))),
            'original_to': str(m.get('X-Original-To', ''))[:2000]}


def content(raw, partial=False):
    m = email.message_from_bytes(raw, policy=email.policy.default)
    part = m.get_body(preferencelist=('plain', 'html'))
    text = ''
    if part:
        try:
            text = part.get_content()
        except (LookupError, UnicodeError):
            text = (part.get_payload(decode=True) or b'').decode('utf-8', 'replace')
        if part.get_content_type() == 'text/html':
            text = re.sub(r'(?is)<(script|style)\b.*?</\1>', '', text)
            text = html.unescape(re.sub(r'<[^>]+>', ' ', text))
    if not isinstance(text, str):
        text = ''
    return {'body': text[:MAX_TEXT], 'truncated': partial or len(text) > MAX_TEXT,
            'attachments': [str(p.get_filename() or '(unnamed)')[:500] for p in m.iter_attachments()][:50]}


class QQ:
    def __init__(self, account):
        self.account = account
        self.deadline = time.monotonic() + ACCOUNT_SECONDS
        self.partial = False
        d = json.loads(Path(account['credential_file']).read_text())
        if d['account'].lower() != account['address'].lower():
            raise ValueError('credential account mismatch')
        self.c = imaplib.IMAP4_SSL('imap.qq.com', 993, ssl_context=ssl.create_default_context(), timeout=15)
        try:
            self.c.login(d['account'], d['authorization_code'])
        except Exception:
            self.close()
            raise

    def close(self):
        with contextlib.suppress(Exception):
            self.c.sock.settimeout(2)
            self.c.logout()

    def folder(self, folder):
        self.c.sock.settimeout(remaining(self.deadline))
        checked(self.c.select(folder, readonly=True))
        validity = int(self.c.response('UIDVALIDITY')[1][0])
        self.c.sock.settimeout(remaining(self.deadline))
        ids = checked(self.c.uid('search', None, 'ALL'))[0].split()
        return validity, [int(v) for v in ids]

    def metadata(self, folder, validity, uid):
        self.c.sock.settimeout(remaining(self.deadline))
        data = checked(self.c.uid('fetch', str(uid), '(INTERNALDATE BODY.PEEK[HEADER.FIELDS (FROM TO SUBJECT DATE MESSAGE-ID X-ORIGINAL-TO)])'))
        pairs = [v for v in data if isinstance(v, tuple)]
        if not pairs:
            return None  # Expunged between search and fetch.
        stamp = re.search(rb'INTERNALDATE "([^"]+)"', pairs[0][0])
        received = int(datetime.datetime.strptime(stamp[1].decode(), '%d-%b-%Y %H:%M:%S %z').timestamp()) if stamp else 0
        return summary(b''.join(v[1] for v in pairs), f'{folder}:{validity}:{uid}', received)

    def sync(self, cursor):
        new = dict(cursor or {})
        messages = []
        warnings = []
        for folder in self.account.get('folders', ['INBOX', 'Junk']):
            if self.deadline - time.monotonic() < 2:
                self.partial = True
                break
            validity, ids = self.folder(folder)
            old = (cursor or {}).get(folder)
            if old is None:
                new[folder] = {'validity': validity, 'uid': max(ids, default=0), 'baseline_at': int(time.time())}
                continue
            if old['validity'] != validity:
                # Identity reset: reconcile retained messages since previous baseline,
                # deduplicate by RFC Message-ID in the host; never silently skip the gap.
                pending = ids
                warnings.append('IMAP identity changed; reconciling retained messages')
            else:
                pending = [v for v in ids if v > old['uid']]
            last = old['uid'] if old['validity'] == validity else 0
            for uid in pending[:100]:
                if self.deadline - time.monotonic() < 2:
                    warnings.append('Source time budget reached; remaining mail retained for next check')
                    break
                m = self.metadata(folder, validity, uid)
                if m and (old['validity'] == validity or m['received_at'] >= old.get('baseline_at', 0)):
                    messages.append(m)
                last = uid
            new[folder] = {'validity': validity, 'uid': last, 'baseline_at': old.get('baseline_at', 0)}
        return messages, new, warnings

    def recent(self, limit):
        out = []
        for folder in self.account.get('folders', ['INBOX', 'Junk']):
            if self.deadline - time.monotonic() < 2:
                self.partial = True
                break
            validity, ids = self.folder(folder)
            for uid in reversed(ids[-limit:]):
                if self.deadline - time.monotonic() < 2:
                    self.partial = True
                    break
                m = self.metadata(folder, validity, uid)
                if m:
                    out.append(m)
        return out

    def read(self, transport_id):
        folder, validity, uid = transport_id.rsplit(':', 2)
        if folder not in self.account.get('folders', ['INBOX', 'Junk']):
            raise ValueError('folder not configured')
        self.c.sock.settimeout(remaining(self.deadline))
        checked(self.c.select(folder, readonly=True))
        current = int(self.c.response('UIDVALIDITY')[1][0])
        if current != int(validity) or not uid.isdigit():
            raise ValueError('message identity expired')
        data = checked(self.c.uid('fetch', uid, f'(RFC822.SIZE BODY.PEEK[]<0.{MAX_RAW}>)'))
        pairs = [v for v in data if isinstance(v, tuple)]
        if not pairs:
            raise ValueError('message no longer available')
        size = re.search(rb'RFC822.SIZE (\d+)', pairs[0][0])
        raw = b''.join(v[1] for v in pairs)
        return content(raw, bool(size and int(size[1]) > len(raw)))


class Gmail:
    ROOT = 'https://gmail.googleapis.com/gmail/v1/users/me/'

    def __init__(self, account):
        self.account = account
        self.deadline = time.monotonic() + ACCOUNT_SECONDS
        self.partial = False
        self.opener = gmail_opener(account)
        p = Path(account['credential_file'])
        with open(str(p) + '.lock', 'a', opener=lambda path, flags: os.open(path, flags, 0o600)) as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            d = json.loads(p.read_text())
            if d['account'].lower() != account['address'].lower():
                raise ValueError('credential account mismatch')
            token = d['token']
            if token.get('expires_at', 0) < time.time() + 120:
                req = urllib.request.Request('https://oauth2.googleapis.com/token', urllib.parse.urlencode({
                    'client_id': d['client_id'], 'client_secret': d['client_secret'],
                    'refresh_token': token['refresh_token'], 'grant_type': 'refresh_token'}).encode())
                with self.opener.open(req, timeout=remaining(self.deadline)) as r:
                    fresh = json.load(r)
                token.update(fresh)
                token['expires_at'] = int(time.time()) + int(fresh.get('expires_in', 3600))
                fd, name = tempfile.mkstemp(prefix='.gmail-refresh-', dir=p.parent)
                try:
                    with os.fdopen(fd, 'w') as f:
                        json.dump(d, f); f.flush(); os.fsync(f.fileno())
                    os.replace(name, p)
                finally:
                    if os.path.exists(name): os.unlink(name)
            self.token = token['access_token']

    def close(self):
        pass

    def get(self, path, **params):
        req = urllib.request.Request(self.ROOT + path + ('?' + urllib.parse.urlencode(params) if params else ''),
                                     headers={'Authorization': 'Bearer ' + self.token})
        with self.opener.open(req, timeout=remaining(self.deadline)) as r:
            raw = r.read(8 * 1024 * 1024 + 1)
        if len(raw) > 8 * 1024 * 1024:
            raise ValueError('response too large')
        return json.loads(raw)

    def metadata(self, mid):
        try:
            d = self.get('messages/' + urllib.parse.quote(mid, safe=''), format='metadata')
        except urllib.error.HTTPError as e:
            if e.code == 404: return None
            raise
        if set(d.get('labelIds', [])) & {'SENT', 'DRAFT', 'TRASH'}:
            return None
        raw = '\r\n'.join(h['name'] + ': ' + h['value'] for h in d.get('payload', {}).get('headers', [])) + '\r\n\r\n'
        m = summary(raw.encode(), mid, int(d.get('internalDate', '0')) // 1000)
        m['snippet'] = html.unescape(d.get('snippet', ''))[:2000]
        return m

    def sync(self, cursor):
        now = int(time.time())
        if not cursor:
            return [], {'history': self.get('profile')['historyId'], 'since': now, 'baseline_since': now}, []
        params = {'startHistoryId': cursor['history'], 'historyTypes': 'messageAdded', 'maxResults': 20}
        if cursor.get('page'): params['pageToken'] = cursor['page']
        recovery = cursor.get('recovery', False)
        warning = []
        try:
            if cursor.get('pending'):
                raise ContinuePage()
            if recovery: raise LookupError()
            d = self.get('history', **params)
            ids = list(dict.fromkeys(m['message']['id'] for h in d.get('history', []) for m in h.get('messagesAdded', [])))
            new = dict(cursor)
            if d.get('nextPageToken'): new['page'] = d['nextPageToken']
            else: new = {'history': d.get('historyId', cursor['history']), 'since': now, 'baseline_since': cursor.get('baseline_since', 0)}
        except ContinuePage:
            ids = cursor['pending']
            new = cursor['after_pending']
            if cursor.get('pending_recovery'): warning.append('Continuing history recovery')
        except (urllib.error.HTTPError, LookupError) as e:
            if isinstance(e, urllib.error.HTTPError) and e.code != 404: raise
            # Snapshot history BEFORE listing; future changes replay idempotently.
            baseline = cursor.get('recovery_history') or self.get('profile')['historyId']
            params = {'q': 'after:' + str(max(0, cursor.get('since', now) - 86400)) + ' -in:sent -in:drafts -in:trash',
                      'includeSpamTrash': 'true', 'maxResults': 20}
            if cursor.get('recovery_page'): params['pageToken'] = cursor['recovery_page']
            d = self.get('messages', **params)
            ids = [m['id'] for m in d.get('messages', [])]
            new = dict(cursor, recovery=True, recovery_history=baseline, recovery_page=d.get('nextPageToken', ''))
            if not d.get('nextPageToken'): new = {'history': baseline, 'since': now, 'baseline_since': cursor.get('baseline_since', 0)}
            warning.append('Gmail history expired; reconciling retained mail with overlap')
        messages = []
        for index, mid in enumerate(ids):
            if self.deadline - time.monotonic() < 2:
                new = dict(cursor, pending=ids[index:], after_pending=new, pending_recovery=bool(warning))
                break
            message = self.metadata(mid)
            if message is not None: messages.append(message)
        if warning:
            messages = [m for m in messages if m['received_at'] >= cursor.get('baseline_since', 0)]
        return messages, new, warning

    def recent(self, limit):
        d = self.get('messages', maxResults=limit, includeSpamTrash='true', q='-in:sent -in:drafts -in:trash')
        out = []
        for item in d.get('messages', []):
            if self.deadline - time.monotonic() < 2:
                self.partial = True
                break
            message = self.metadata(item['id'])
            if message is not None: out.append(message)
        return out

    def read(self, mid):
        d = self.get('messages/' + urllib.parse.quote(mid, safe=''), format='full')
        texts = {'text/plain': [], 'text/html': []}
        attachments = []
        partial = False
        def walk(part):
            nonlocal partial
            if part.get('filename'):
                attachments.append(part['filename'][:500])
            elif part.get('mimeType') in texts:
                data = part.get('body', {}).get('data', '')
                if not data and part.get('body', {}).get('attachmentId'):
                    partial = True
                raw = base64.urlsafe_b64decode(data + '===')
                header = next((h['value'] for h in part.get('headers', []) if h['name'].lower() == 'content-type'), part['mimeType'])
                parsed = email.message_from_string('Content-Type: ' + header + '\n\n')
                charset = parsed.get_content_charset() or 'utf-8'
                try: text = raw.decode(charset, 'replace')
                except LookupError: text = raw.decode('utf-8', 'replace')
                texts[part['mimeType']].append(text)
            for child in part.get('parts', []): walk(child)
        walk(d.get('payload', {}))
        text = '\n'.join(texts['text/plain'])
        if not text:
            text = '\n'.join(texts['text/html'])
            text = re.sub(r'(?is)<(script|style)\b.*?</\1>', '', text)
            text = html.unescape(re.sub(r'<[^>]+>', ' ', text))
        return {'body': text[:MAX_TEXT], 'truncated': partial or len(text) > MAX_TEXT, 'attachments': attachments[:50]}


def account_request(account, request):
    conn = None
    try:
        conn = QQ(account) if account['provider'] == 'qq' else Gmail(account)
        if request['action'] == 'sync':
            messages, cursor, warnings = conn.sync(request.get('cursors', {}).get(account['id']))
            if conn.partial: warnings.append('Source time budget reached; remaining folders retained for next check')
            return {'account': account['id'], 'ok': True, 'messages': messages, 'cursor': cursor, 'warnings': warnings}
        if request['action'] == 'recent':
            return {'account': account['id'], 'ok': True, 'messages': conn.recent(request.get('limit', 20)), 'warnings': ['Recent scan is partial due to source time budget'] if conn.partial else []}
        if request['action'] == 'read':
            return {'account': account['id'], 'ok': True, **conn.read(request['transport_id'])}
        raise ValueError('unsupported action')
    except Exception as e:
        # Exceptions may contain tokens, addresses, URLs or server text. Never return them.
        category = 'authentication_failed' if isinstance(e, imaplib.IMAP4.error) else 'connection_or_protocol_failed'
        if isinstance(e, urllib.error.HTTPError):
            category = 'authentication_failed' if e.code in (400, 401, 403) else 'upstream_failed'
        return {'account': account['id'], 'ok': False, 'error': category}
    finally:
        if conn: conn.close()


def main():
    request = json.load(sys.stdin)
    accounts = request['accounts']
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        results = list(pool.map(lambda a: account_request(a, request), accounts))
    print(json.dumps({'results': results}, ensure_ascii=False))


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print(json.dumps({'error': 'mail transport unavailable'}))
        sys.exit(1)
