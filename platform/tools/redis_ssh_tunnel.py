#!/usr/bin/env python3
"""Local-only RedisInsight relay over SSH to the server's private Redis socket."""
import asyncio
import shlex

REMOTE = '''import os,select,socket,sys
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
s.connect('/run/asrobot-login-redis/redis.sock')
inputs=[s,0]
while s in inputs:
 ready,_,_=select.select(inputs,[],[])
 for source in ready:
  if source==0:
   data=os.read(0,65536)
   if data:s.sendall(data)
   else:inputs.remove(0);s.shutdown(socket.SHUT_WR)
  else:
   data=s.recv(65536)
   if not data:inputs.remove(s);break
   sys.stdout.buffer.write(data);sys.stdout.buffer.flush()
s.close()
'''
COMMAND = 'sudo -n /usr/bin/python3 -u -c ' + shlex.quote(REMOTE)
active = 0

async def connect(reader, writer):
    global active
    if active >= 16:
        writer.close()
        return
    active += 1
    process = None
    try:
        process = await asyncio.create_subprocess_exec(
            '/usr/bin/ssh', '-T', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=8',
            '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=3',
            'tencent', COMMAND, stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE)
        async def pump(source, target):
            while True:
                data = await source.read(65536)
                if not data:
                    break
                target.write(data)
                await target.drain()
        tasks = [asyncio.create_task(pump(reader, process.stdin)),
                 asyncio.create_task(pump(process.stdout, writer))]
        try:
            await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
        finally:
            for task in tasks:
                task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
    except (OSError, ConnectionError, asyncio.CancelledError):
        pass
    finally:
        writer.close()
        if process and process.returncode is None:
            process.terminate()
            try:
                await asyncio.wait_for(process.wait(), timeout=3)
            except asyncio.TimeoutError:
                process.kill()
                await process.wait()
        active -= 1

async def main():
    server = await asyncio.start_server(connect, '127.0.0.1', 16380)
    async with server:
        await server.serve_forever()

if __name__ == '__main__':
    asyncio.run(main())
