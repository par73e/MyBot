"""Optional cache-aside for a small allowlist of owner-scoped native reads."""
import asyncio
import hashlib
import json
import logging
import os
import random
import time
import uuid

import redis.asyncio as redis
from redis.exceptions import RedisError

log = logging.getLogger(__name__)
MAX_TTL = 60
MAX_BYTES = 256 * 1024
READ_ACTIONS = {'list', 'get', 'status', 'query', 'search', 'read', 'silent',
                'digest_prompt', 'latest', 'preview', 'modules', 'runs', 'get_default', 'history'}
POLICIES = {
    ('assistant_ai_news', 'list'): 20,
    ('assistant_ai_news', 'read'): 60,
    ('assistant_github', 'status'): 10,
    ('assistant_github', 'query'): 10,
    ('assistant_morning', 'latest'): 10,
    ('assistant_evening', 'latest'): 10,
}

def policy(endpoint, body):
    if endpoint == 'tools' and body is None:
        return 60
    if endpoint != 'tool' or not isinstance(body, dict) or set(body) != {'tool', 'args'}:
        return 0
    args = body.get('args')
    if not isinstance(args, dict) or not isinstance(body.get('tool'), str) or not isinstance(args.get('action'), str):
        return 0
    return POLICIES.get((body.get('tool'), args.get('action')), 0)

def mutates(endpoint, body):
    if endpoint == 'chat':
        return True
    if body is None:
        return False
    if endpoint == 'tool':
        args = body.get('args', {}) if isinstance(body, dict) else {}
        return not isinstance(args, dict) or not isinstance(args.get('action'), str) or args.get('action') not in READ_ACTIONS
    return True

class PageCache:
    def __init__(self):
        self.client = None
        self.url = ''
        self.bypass_until = 0
        self.locks = [asyncio.Lock() for _ in range(32)]

    def available(self):
        return bool(os.environ.get('ASROBOT_PAGE_CACHE_URL')) and time.monotonic() >= self.bypass_until

    async def connection(self):
        url = os.environ.get('ASROBOT_PAGE_CACHE_URL', '')
        if url != self.url:
            if self.client:
                await self.client.aclose()
            self.url = url
            self.client = redis.Redis.from_url(url, decode_responses=True,
                socket_connect_timeout=0.075, socket_timeout=0.075, max_connections=16)
        return self.client

    def failed(self, invalidation=False):
        # A failed invalidation quarantines cached reads until every old value
        # has expired, even if Redis reconnects sooner. Source writes still win.
        self.bypass_until = max(self.bypass_until, time.monotonic() + (MAX_TTL + 5 if invalidation else 3))
        log.warning('Page cache temporarily bypassed; native reads remain authoritative')

    @staticmethod
    def partition(uid, source):
        return 'asrobot:pages:v1:' + hashlib.sha256(json.dumps([uid, source]).encode()).hexdigest()

    async def invalidate(self, uid, source):
        if not os.environ.get('ASROBOT_PAGE_CACHE_URL'):
            return
        try:
            client = await self.connection()
            await client.set(self.partition(uid, source) + ':generation', uuid.uuid4().hex, ex=MAX_TTL + 60)
        except (RedisError, OSError, ValueError, UnicodeError):
            self.failed(invalidation=True)

    async def read(self, uid, source, endpoint, body, loader, fresh=False):
        ttl = policy(endpoint, body)
        if not ttl or not self.available():
            return await loader()
        prefix = self.partition(uid, source)
        args = json.dumps([endpoint, body], sort_keys=True, separators=(',', ':'), ensure_ascii=False)
        signature = hashlib.sha256(args.encode()).hexdigest()
        stripe = hashlib.sha256((prefix + signature).encode()).hexdigest()
        async with self.locks[int(stripe[:8], 16) % len(self.locks)]:
            if not self.available():
                return await loader()
            key = None
            try:
                client = await self.connection()
                generation_key = prefix + ':generation'
                await client.set(generation_key, uuid.uuid4().hex, nx=True, ex=MAX_TTL + 60)
                generation = await client.get(generation_key)
                if not generation or len(generation) != 32:
                    raise ValueError('invalid cache generation')
                key = prefix + ':' + generation + ':' + signature
                raw = None if fresh else await client.get(key)
                if raw is not None:
                    try:
                        if len(raw.encode()) > MAX_BYTES:
                            raise ValueError('oversized cache entry')
                        result = json.loads(raw)
                        if result['schema'] != 1 or not isinstance(result['value'], (dict, list)):
                            raise ValueError('invalid cache entry')
                        return result['value']
                    except (ValueError, KeyError, TypeError):
                        await client.delete(key)
            except (RedisError, OSError, ValueError, UnicodeError):
                self.failed()
                key = None
            result = await loader()
            if key and self.available() and isinstance(result, (dict, list)) and not (isinstance(result, dict) and 'error' in result):
                try:
                    raw = json.dumps({'schema': 1, 'value': result}, ensure_ascii=False, allow_nan=False)
                except (TypeError, ValueError):
                    return result
                if len(raw.encode()) <= MAX_BYTES:
                    try:
                        # Jitter only shortens TTL, so the documented staleness
                        # bound and failed-invalidation quarantine remain true.
                        await client.set(key, raw, ex=max(1, ttl - random.randint(0, max(1, ttl // 10))))
                    except (RedisError, OSError):
                        self.failed()
            return result

    async def close(self):
        if self.client:
            await self.client.aclose()
        self.client = None
        self.url = ''
        self.locks = [asyncio.Lock() for _ in range(32)]

cache = PageCache()
