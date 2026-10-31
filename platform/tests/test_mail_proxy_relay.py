import asyncio
import importlib.util
import os
from pathlib import Path
import socket
import sys
import tempfile
import threading
import pytest

ROOT = Path(__file__).parents[2]
NATIVE = ROOT/'platform/nativebridge'
if not (NATIVE/'mail_proxy_relay.py').is_file() or not (ROOT/'reasonix-assistant-v1.39.10/internal/assistant/mail_transport.py').is_file():
    pytest.skip('mail transport source is tested separately from the platform image', allow_module_level=True)
sys.path.insert(0, str(NATIVE))
spec = importlib.util.spec_from_file_location('mail_proxy_relay', NATIVE/'mail_proxy_relay.py')
relay = importlib.util.module_from_spec(spec)
spec.loader.exec_module(relay)
spec2 = importlib.util.spec_from_file_location('mail_transport', ROOT/'reasonix-assistant-v1.39.10/internal/assistant/mail_transport.py')
transport = importlib.util.module_from_spec(spec2)
spec2.loader.exec_module(transport)


def test_relay_only_connects_to_approved_google_hosts():
    async def exercise():
        forwarded = []
        async def upstream(reader, writer):
            forwarded.append(await reader.readuntil(b'\r\n\r\n'))
            writer.write(b'HTTP/1.1 200 Connection Established\r\n\r\n')
            await writer.drain()
            data = await reader.read(100)
            writer.write(b'echo:'+data)
            await writer.drain()
            writer.close()
            await writer.wait_closed()
        server = await asyncio.start_server(upstream, '127.0.0.1', 0)
        port = server.sockets[0].getsockname()[1]
        with tempfile.TemporaryDirectory(prefix='asr-proxy-', dir='/tmp') as directory:
            path = str(Path(directory)/'relay.sock')
            bridge = await asyncio.start_unix_server(lambda r,w:relay.relay_connection(r,w,port), path=path, limit=relay.MAX_HEADER+1)
            try:
                reader, writer = await asyncio.open_unix_connection(path)
                writer.write(b'CONNECT gmail.googleapis.com:443 HTTP/1.1\r\nHost: ignored\r\n\r\n')
                await writer.drain()
                assert b' 200 ' in await reader.readuntil(b'\r\n\r\n')
                writer.write(b'hello'); await writer.drain()
                assert await reader.read(100) == b'echo:hello'
                writer.close(); await writer.wait_closed()
                reader, writer = await asyncio.open_unix_connection(path)
                writer.write(b'CONNECT 127.0.0.1:22 HTTP/1.1\r\n\r\n')
                await writer.drain()
                assert b' 403 ' in await reader.readuntil(b'\r\n\r\n')
                writer.close(); await writer.wait_closed()
                assert len(forwarded) == 1 and b'gmail.googleapis.com:443' in forwarded[0]
            finally:
                bridge.close(); await bridge.wait_closed()
        server.close(); await server.wait_closed()
    asyncio.run(exercise())


def test_unix_https_transport_uses_tunnel_and_preserves_tls_hostname():
    with tempfile.TemporaryDirectory(prefix='asr-mail-', dir='/tmp') as directory:
        path = str(Path(directory)/'proxy.sock')
        listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        listener.bind(path); listener.listen(1)
        accepted = []
        thread = threading.Thread(target=lambda:accepted.append(listener.accept()[0]))
        thread.start()
        names = []
        class Context:
            def wrap_socket(self, sock, server_hostname):
                names.append(server_hostname)
                return sock
        conn = transport.UnixProxyHTTPSConnection('placeholder.invalid', proxy_socket=path, timeout=2)
        conn._context = Context()
        conn.set_tunnel('gmail.googleapis.com', 443)
        conn._tunnel = lambda:None
        try:
            conn.connect()
            thread.join(timeout=2)
            assert names == ['gmail.googleapis.com'] and accepted
        finally:
            conn.close()
            for sock in accepted:sock.close()
            listener.close()


def test_isolated_local_proxy_requires_matching_private_relay(monkeypatch):
    root='/var/lib/asrobot-instances/'+'a'*32
    monkeypatch.setenv('ASROBOT_INSTANCE_MODE','isolated')
    monkeypatch.setenv('ASROBOT_RUNTIME_ROOT',root)
    monkeypatch.setenv('ASROBOT_RUNTIME_STATE',root+'/state')
    monkeypatch.setenv('ASROBOT_MAIL_PROXY_SOCKET',root+'/state/mail-proxy.sock')
    monkeypatch.setenv('ASROBOT_MAIL_PROXY_PORT','17890')
    opener = transport.gmail_opener({'proxy':'http://127.0.0.1:17890'})
    assert any(isinstance(h, transport.FixedUnixProxyHandler) for h in opener.handlers)
    request = transport.urllib.request.Request('https://gmail.googleapis.com/gmail/v1/users/me/profile')
    transport.FixedUnixProxyHandler().https_request(request)
    assert request._tunnel_host == 'gmail.googleapis.com'
    handler = transport.UnixProxyHTTPSHandler(root+'/state/mail-proxy.sock')
    monkeypatch.setattr(handler, 'do_open', lambda factory, request, **kwargs:kwargs)
    assert 'context' in handler.https_open(request)
    with pytest.raises(ValueError, match='private relay'):
        transport.gmail_opener({'proxy':'http://127.0.0.1:12345'})
    monkeypatch.delenv('ASROBOT_MAIL_PROXY_SOCKET')
    with pytest.raises(ValueError, match='private relay'):
        transport.gmail_opener({'proxy':'http://127.0.0.1:17890'})
