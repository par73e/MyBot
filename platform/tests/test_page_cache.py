import asyncio
import json
import time

import pytest
from redis.exceptions import ConnectionError
from app.page_cache import PageCache, policy

class FakeRedis:
    def __init__(self):self.data={};self.expiry={};self.fail=False;self.fail_writes=False
    async def get(self,key):
        if self.fail:raise ConnectionError('offline')
        if self.expiry.get(key,0)<=time.monotonic():self.data.pop(key,None)
        return self.data.get(key)
    async def set(self,key,value,ex,nx=False):
        if self.fail or self.fail_writes:raise ConnectionError('offline')
        if nx and await self.get(key) is not None:return False
        self.data[key]=value;self.expiry[key]=time.monotonic()+ex;return True
    async def delete(self,key):self.data.pop(key,None)

@pytest.fixture
def cache(monkeypatch):
    monkeypatch.setenv('ASROBOT_PAGE_CACHE_URL','unix:///unused')
    c=PageCache();fake=FakeRedis()
    async def connection():return fake
    monkeypatch.setattr(c,'connection',connection)
    return c,fake

BODY={'tool':'assistant_github','args':{'action':'status'}}

def test_scoping_arguments_ttl_and_fresh(cache):
    c,r=cache;calls=[]
    async def load():calls.append(1);return {'n':len(calls)}
    async def check():
        assert await c.read('a','instance:one','tool',BODY,load)=={'n':1}
        assert await c.read('a','instance:one','tool',BODY,load)=={'n':1}
        assert await c.read('b','instance:one','tool',BODY,load)=={'n':2}
        assert await c.read('a','instance:two','tool',BODY,load)=={'n':3}
        assert await c.read('a','instance:one','tool',BODY,load,fresh=True)=={'n':4}
        assert await c.read('a','instance:one','tool',BODY,load)=={'n':4}
        for key in list(r.data):
            if not key.endswith(':generation'):r.expiry[key]=0
        assert await c.read('a','instance:one','tool',BODY,load)=={'n':5}
    asyncio.run(check())

def test_invalidation_races_and_recovered_cache_cannot_resurrect_old_data(cache):
    c,r=cache;calls=[]
    async def load():calls.append(1);return {'n':len(calls)}
    async def check():
        await c.read('a','source','tool',BODY,load)
        await c.invalidate('a','source')
        assert await c.read('a','source','tool',BODY,load)=={'n':2}
        r.fail_writes=True;await c.invalidate('a','source');r.fail_writes=False
        assert await c.read('a','source','tool',BODY,load)=={'n':3}
        assert c.bypass_until>time.monotonic()+60
    asyncio.run(check())

def test_failures_corrupt_values_and_errors_fall_back(cache):
    c,r=cache;calls=[]
    async def load():calls.append(1);return {'n':len(calls)}
    async def check():
        await c.read('a','source','tool',BODY,load)
        for k in r.data:
            if not k.endswith(':generation'):r.data[k]='invalid json'
        assert await c.read('a','source','tool',BODY,load)=={'n':2}
        r.fail=True
        assert await c.read('a','source','tool',BODY,load)=={'n':3}
    asyncio.run(check())

def test_concurrent_cache_misses_coalesce(cache):
    c,r=cache;calls=[]
    async def load():calls.append(1);await asyncio.sleep(.01);return {'n':len(calls)}
    async def check():
        result=await asyncio.gather(*(c.read('a','source','tool',BODY,load) for _ in range(8)))
        assert result==[{'n':1}]*8
        assert len(calls)==1
    asyncio.run(check())

def test_only_explicit_read_allowlist():
    for tool in ['assistant_tasks','assistant_memory','assistant_server_health','assistant_weather_alerts','assistant_heartbeat']:
        assert policy('tool',{'tool':tool,'args':{'action':'status'}})==0
    for endpoint in ['profile','identity','status','mail-accounts','weather-setup','github-credential']:
        assert policy(endpoint,None)==0
    assert policy('tool',{'tool':'assistant_github','args':{'action':'add'}})==0
    assert policy('tools',None)==60

def test_write_during_inflight_read_cannot_repopulate_current_generation(cache):
    c,r=cache
    async def check():
        started=asyncio.Event();finish=asyncio.Event()
        async def old():started.set();await finish.wait();return {'version':'old'}
        task=asyncio.create_task(c.read('a','source','tool',BODY,old))
        await started.wait();await c.invalidate('a','source');finish.set();await task
        async def latest():return {'version':'new'}
        assert await c.read('a','source','tool',BODY,latest)=={'version':'new'}
    asyncio.run(check())

def test_redis_integration_hit_expiry_isolation_and_outage(monkeypatch):
    import shutil,subprocess,tempfile
    from pathlib import Path
    server=shutil.which('redis-server')
    if not server:pytest.skip('redis-server unavailable')
    with tempfile.TemporaryDirectory(prefix='asr-cache-',dir='/private/tmp') as folder:
        sock=Path(folder)/'r.sock'
        proc=subprocess.Popen([server,'--port','0','--unixsocket',str(sock),'--unixsocketperm','700','--save','','--appendonly','no','--maxmemory','8mb','--maxmemory-policy','allkeys-lru'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        try:
            for _ in range(100):
                if sock.exists():break
                time.sleep(.01)
            assert sock.exists()
            monkeypatch.setenv('ASROBOT_PAGE_CACHE_URL','unix://'+str(sock)+'?db=0')
            c=PageCache();calls=[]
            async def loader():calls.append(1);await asyncio.sleep(.01);return {'calls':len(calls)}
            async def check():
                first=await c.read('owner','instance:1','tool',BODY,loader)
                assert await c.read('owner','instance:1','tool',BODY,loader)==first
                assert len(calls)==1
                await c.read('other','instance:1','tool',BODY,loader);assert len(calls)==2
                client=await c.connection()
                keys=await client.keys('asrobot:pages:v1:*')
                assert keys
                for key in keys:assert await client.ttl(key)>0
                await c.invalidate('owner','instance:1')
                await c.read('owner','instance:1','tool',BODY,loader);assert len(calls)==3
                proc.terminate();proc.wait(timeout=3)
                start=time.perf_counter()
                assert await c.read('owner','instance:1','tool',BODY,loader)=={'calls':4}
                assert time.perf_counter()-start<.5
                await c.close()
            asyncio.run(check())
        finally:
            if proc.poll() is None:proc.terminate();proc.wait(timeout=3)
