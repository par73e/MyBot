"""Bound expensive password work without letting cancelled requests free slots early."""
import asyncio
import threading
import weakref
from fastapi import HTTPException

_gates=weakref.WeakKeyDictionary()
_guard=threading.Lock()

async def password_work(function,*args):
    loop=asyncio.get_running_loop()
    with _guard:
        gate=_gates.setdefault(loop,asyncio.Semaphore(2))
    try:
        await asyncio.wait_for(gate.acquire(),timeout=.05)
    except asyncio.TimeoutError:
        raise HTTPException(503,'登录校验繁忙，请稍后重试') from None
    job=asyncio.create_task(asyncio.to_thread(function,*args))
    # A disconnected caller must not release capacity while its worker still runs.
    def finished(task):
        gate.release()
        if not task.cancelled():task.exception()
    job.add_done_callback(finished)
    return await asyncio.shield(job)
