"""Private Gmail-only CONNECT relay to an operator-selected local proxy.

The assistant reaches this process over its own Unix socket. The relay runs in
a separate service; it never accepts arbitrary proxy destinations or commands.
"""
import asyncio
import ctypes
import os
from pathlib import Path
import re
import signal

from instance_supervisor import remove_stale_owned_socket

ALLOWED = {'gmail.googleapis.com:443', 'oauth2.googleapis.com:443'}
MAX_HEADER = 8192
MAX_STREAM = 64 * 1024 * 1024


async def relay_connection(reader, writer, upstream_port):
    upstream_writer = None
    sent_response = False
    try:
        header = await asyncio.wait_for(reader.readuntil(b'\r\n\r\n'), 5)
        if len(header) > MAX_HEADER:
            raise ValueError('header too large')
        first = header.split(b'\r\n', 1)[0].decode('ascii')
        match = re.fullmatch(r'CONNECT ([A-Za-z0-9.-]+:443) HTTP/1\.[01]', first)
        if not match or match.group(1).lower() not in ALLOWED:
            writer.write(b'HTTP/1.1 403 Forbidden\r\nConnection: close\r\n\r\n')
            await writer.drain()
            sent_response = True
            return
        target = match.group(1).lower()
        upstream_reader, upstream_writer = await asyncio.wait_for(asyncio.open_connection('127.0.0.1', upstream_port), 5)
        upstream_writer.write(('CONNECT '+target+' HTTP/1.1\r\nHost: '+target+'\r\n\r\n').encode('ascii'))
        await upstream_writer.drain()
        response = await asyncio.wait_for(upstream_reader.readuntil(b'\r\n\r\n'), 10)
        if len(response) > MAX_HEADER or not re.match(rb'^HTTP/1\.[01] 200(?: |\r\n)', response):
            raise ValueError('upstream proxy refused connection')
        writer.write(b'HTTP/1.1 200 Connection Established\r\n\r\n')
        await writer.drain()
        sent_response = True
        async def pump(source, destination):
            total = 0
            while True:
                chunk = await source.read(65536)
                if not chunk:
                    return
                total += len(chunk)
                if total > MAX_STREAM:
                    raise ValueError('stream limit exceeded')
                destination.write(chunk)
                await destination.drain()
        tasks = [asyncio.create_task(pump(reader, upstream_writer)), asyncio.create_task(pump(upstream_reader, writer))]
        try:
            done, _ = await asyncio.wait(tasks, timeout=90, return_when=asyncio.FIRST_COMPLETED)
            if not done:
                raise asyncio.TimeoutError()
            for task in done:
                task.result()
        finally:
            for task in tasks:
                task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
    except (OSError, UnicodeError, ValueError, asyncio.TimeoutError, asyncio.IncompleteReadError, asyncio.LimitOverrunError):
        if not sent_response:
            writer.write(b'HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n')
    finally:
        if upstream_writer:
            upstream_writer.close()
            try: await upstream_writer.wait_closed()
            except OSError: pass
        writer.close()
        try: await writer.wait_closed()
        except OSError: pass


async def main():
    root = Path(os.environ.get('ASROBOT_RUNTIME_ROOT', ''))
    state = Path(os.environ.get('ASROBOT_RUNTIME_STATE', ''))
    port_text = os.environ.get('ASROBOT_MAIL_PROXY_PORT', '')
    if (os.environ.get('ASROBOT_INSTANCE_MODE') != 'isolated' or root.parent != Path('/var/lib/asrobot-instances') or
            not re.fullmatch(r'[0-9a-f]{32}', root.name) or state != root/'state' or
            not port_text.isdigit() or not 1 <= int(port_text) <= 65535):
        raise SystemExit('invalid instance relay configuration')
    # The owner process cannot inspect or modify this relay through ptrace.
    if ctypes.CDLL(None, use_errno=True).prctl(4, 0, 0, 0, 0) != 0:  # PR_SET_DUMPABLE
        raise SystemExit('cannot protect relay process')
    socket_path = state/'mail-proxy.sock'
    remove_stale_owned_socket(socket_path)
    server = await asyncio.start_unix_server(lambda r,w: relay_connection(r,w,int(port_text)), path=str(socket_path), limit=MAX_HEADER+1)
    os.chmod(socket_path, 0o600)
    inode = socket_path.lstat()
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stop.set)
    try:
        await stop.wait()
    finally:
        server.close()
        await server.wait_closed()
        try:
            current = socket_path.lstat()
            if current.st_ino == inode.st_ino and current.st_dev == inode.st_dev:
                socket_path.unlink()
        except FileNotFoundError:
            pass


if __name__ == '__main__':
    asyncio.run(main())
