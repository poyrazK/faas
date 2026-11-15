import asyncio
import threading
import time

import pytest

from faas_sdk.lifecycle_runtime import (
    DEFAULT_AFTER_RESTORE_PATH,
    DEFAULT_BEFORE_CHECKPOINT_PATH,
    LifecycleHookError,
    LifecycleHooks,
)


def asgi_call(app, path, method="POST", headers=(), client=("127.0.0.1", 5000)):
    sent = []

    async def receive():
        return {"type": "http.request", "body": b"", "more_body": False}

    async def send(message):
        sent.append(message)

    scope = {"type": "http", "path": path, "method": method, "client": client,
             "headers": [(k.encode(), v.encode()) for k, v in headers]}
    asyncio.run(app(scope, receive, send))
    return sent[0]["status"] if sent else None


def wsgi_call(app, path, method="POST", headers=None, remote="127.0.0.1"):
    environ = {"PATH_INFO": path, "REQUEST_METHOD": method, "REMOTE_ADDR": remote, **(headers or {})}
    status = []
    app(environ, lambda line, _headers: status.append(int(line.split()[0])))
    return status[0]


async def passthrough(scope, receive, send):
    await send({"type": "http.response.start", "status": 418, "headers": []})
    await send({"type": "http.response.body", "body": b""})


def test_asgi_serves_only_marked_loopback_posts():
    hooks = LifecycleHooks()
    restored = []

    @hooks.on_after_restore
    async def refresh():
        restored.append(1)

    app = hooks.asgi(passthrough)
    marker = [("X-Faas-After-Restore", "1")]
    assert asgi_call(app, DEFAULT_AFTER_RESTORE_PATH, headers=marker) == 204
    assert asgi_call(app, DEFAULT_AFTER_RESTORE_PATH, headers=marker, client=("::ffff:127.0.0.1", 1)) == 204
    assert asgi_call(app, DEFAULT_AFTER_RESTORE_PATH) == 403
    assert asgi_call(app, DEFAULT_AFTER_RESTORE_PATH, headers=marker, client=("10.0.0.7", 1)) == 403
    assert asgi_call(app, DEFAULT_AFTER_RESTORE_PATH, method="GET", headers=marker) == 405
    assert asgi_call(app, "/v1/query") == 418
    assert len(restored) == 2


def test_asgi_failure_and_budget():
    failing = LifecycleHooks()
    failing.on_before_checkpoint(lambda: (_ for _ in ()).throw(RuntimeError("flush failed")))
    slow = LifecycleHooks(budget_seconds=0.05)

    @slow.on_before_checkpoint
    async def wait():
        await asyncio.sleep(1)

    marker = [("x-faas-before-checkpoint", "1")]
    for hooks in (failing, slow):
        assert asgi_call(hooks.asgi(passthrough), DEFAULT_BEFORE_CHECKPOINT_PATH, headers=marker) == 500


def test_async_runs_are_shared():
    hooks = LifecycleHooks()
    runs = []

    @hooks.on_after_restore
    async def refresh():
        runs.append(1)
        await asyncio.sleep(0.05)

    async def main():
        await asyncio.gather(hooks.run_async("after_restore"), hooks.run_async("after_restore"))
        await hooks.run_async("after_restore")

    asyncio.run(main())
    assert len(runs) == 2


def test_wsgi_and_shared_sync_runs():
    hooks = LifecycleHooks()
    runs = []
    gate = threading.Event()

    @hooks.on_after_restore
    def refresh():
        runs.append(1)
        gate.wait(1)

    app = hooks.wsgi(lambda environ, start: (start("418 Teapot", []), [b""])[1])
    marker = {"HTTP_X_FAAS_AFTER_RESTORE": "1"}
    results = []
    threads = [threading.Thread(target=lambda: results.append(wsgi_call(app, DEFAULT_AFTER_RESTORE_PATH, headers=marker)))
               for _ in range(3)]
    for thread in threads:
        thread.start()
    time.sleep(0.05)
    gate.set()
    for thread in threads:
        thread.join()
    assert results == [204, 204, 204]
    assert len(runs) == 1
    assert wsgi_call(app, DEFAULT_AFTER_RESTORE_PATH) == 403
    assert wsgi_call(app, DEFAULT_AFTER_RESTORE_PATH, headers=marker, remote="10.1.2.3") == 403
    assert wsgi_call(app, "/other") == 418


def test_wsgi_budget_and_coroutine_refusal():
    slow = LifecycleHooks(budget_seconds=0.05)
    slow.on_before_checkpoint(lambda: time.sleep(0.5))
    with pytest.raises(LifecycleHookError):
        slow.run_sync("before_checkpoint")

    coroutine = LifecycleHooks()

    @coroutine.on_after_restore
    async def refresh():
        pass

    with pytest.raises(LifecycleHookError):
        coroutine.run_sync("after_restore")


def _overlap_counter(gate):
    state = {"runs": 0, "running": 0, "max": 0}
    lock = threading.Lock()

    def slow():
        with lock:
            state["runs"] += 1
            state["running"] += 1
            state["max"] = max(state["max"], state["running"])
        gate.wait(2)  # ignores the budget, like a blocking reconnect
        with lock:
            state["running"] -= 1

    return state, slow


def test_sync_retry_after_budget_does_not_overlap():
    # Review finding 10.
    hooks, gate = LifecycleHooks(budget_seconds=0.03), threading.Event()
    state, slow = _overlap_counter(gate)
    hooks.on_after_restore(slow)
    for _ in range(2):
        with pytest.raises(LifecycleHookError):
            hooks.run_sync("after_restore")
    gate.set()
    time.sleep(0.05)
    assert state["runs"] == 1 and state["max"] == 1
    hooks.run_sync("after_restore")
    assert state["runs"] == 2


def test_async_retry_after_budget_does_not_overlap():
    hooks, gate = LifecycleHooks(budget_seconds=0.03), threading.Event()
    state, slow = _overlap_counter(gate)
    hooks.on_after_restore(slow)  # a sync callback runs in a thread under ASGI

    async def main():
        for _ in range(2):
            with pytest.raises(LifecycleHookError):
                await hooks.run_async("after_restore")
        gate.set()
        await asyncio.sleep(0.05)
        assert state["runs"] == 1 and state["max"] == 1
        await hooks.run_async("after_restore")
        assert state["runs"] == 2

    asyncio.run(main())


def test_recycle_helpers():
    class Engine:
        disposed = 0

        def dispose(self):
            self.disposed += 1

    class Pool:
        expired = 0

        async def expire_connections(self):
            self.expired += 1

    hooks, engine, pool = LifecycleHooks(), Engine(), Pool()
    hooks.recycle_sqlalchemy(engine)
    hooks.expire_asyncpg(pool)

    async def main():
        await hooks.run_async("before_checkpoint")
        await hooks.run_async("after_restore")

    asyncio.run(main())
    assert engine.disposed == 2 and pool.expired == 2


def test_rejects_non_positive_budget():
    with pytest.raises(LifecycleHookError):
        LifecycleHooks(budget_seconds=0)
