"""Gregale lifecycle hooks (ADR-20261106105015 §2).

Serves the gregale.yaml ``lifecycle.before_checkpoint`` and
``lifecycle.after_restore`` callbacks (docs/cold-wake.md) for ASGI and WSGI
apps, with the platform's header, loopback and time checks built in::

    from faas_sdk.lifecycle_runtime import LifecycleHooks

    hooks = LifecycleHooks()
    hooks.recycle_sqlalchemy(engine)          # or hooks.expire_asyncpg(pool)

    @hooks.on_after_restore
    async def refresh() -> None:
        ...

    app = hooks.asgi(app)                     # FastAPI/Starlette; hooks.wsgi(app) for Flask/Django

Secrets keep their own SIGHUP path through ``SecretReloadCoordinator``; do not
reload them here. Standard library only.
"""

from __future__ import annotations

import asyncio
import inspect
import ipaddress
import threading
from collections.abc import Awaitable, Callable
from typing import Any

DEFAULT_BEFORE_CHECKPOINT_PATH = "/internal/before-checkpoint"
DEFAULT_AFTER_RESTORE_PATH = "/internal/after-restore"
#: Headroom under the platform's 2 s hook ceiling.
DEFAULT_LIFECYCLE_HOOK_BUDGET_SECONDS = 1.5

Callback = Callable[[], "None | Awaitable[None]"]

_MARKERS = {
    "before_checkpoint": "x-faas-before-checkpoint",
    "after_restore": "x-faas-after-restore",
}


class LifecycleHookError(RuntimeError):
    """A lifecycle callback failed or exceeded the budget."""


class LifecycleHooks:
    """Answers only loopback POSTs carrying the platform's marker header, runs
    the registered callbacks in order under one budget, and shares one run
    between overlapping deliveries."""

    def __init__(
        self,
        before_checkpoint_path: str = DEFAULT_BEFORE_CHECKPOINT_PATH,
        after_restore_path: str = DEFAULT_AFTER_RESTORE_PATH,
        budget_seconds: float = DEFAULT_LIFECYCLE_HOOK_BUDGET_SECONDS,
    ) -> None:
        if not isinstance(budget_seconds, (int, float)) or not budget_seconds > 0:
            raise LifecycleHookError("budget_seconds must be a positive number")
        self.paths = {before_checkpoint_path: "before_checkpoint", after_restore_path: "after_restore"}
        self.budget_seconds = float(budget_seconds)
        self._callbacks: dict[str, list[Callback]] = {"before_checkpoint": [], "after_restore": []}
        self._lock = threading.Lock()
        self._async_runs: dict[str, asyncio.Task[None]] = {}
        self._sync_runs: dict[str, tuple[threading.Event, list[BaseException]]] = {}

    # Registration -------------------------------------------------------

    def on_before_checkpoint(self, callback: Callback) -> Callback:
        """Work before Gregale captures a new init snapshot; raising refuses the capture."""
        self._callbacks["before_checkpoint"].append(callback)
        return callback

    def on_after_restore(self, callback: Callback) -> Callback:
        """Work when an instance restores or resumes frozen. Raising discards the
        instance and the request falls back to a fresh boot. Keep it short."""
        self._callbacks["after_restore"].append(callback)
        return callback

    def recycle_sqlalchemy(self, engine: Any) -> None:
        """Close an SQLAlchemy engine's pooled connections before a checkpoint and
        after a restore; their sockets point at connections the database closed
        while the instance was parked or frozen. Closing is local and fast."""
        def dispose() -> None:
            engine.dispose()
        self.on_before_checkpoint(dispose)
        self.on_after_restore(dispose)

    def expire_asyncpg(self, pool: Any) -> None:
        """Expire an asyncpg pool's connections before a checkpoint and after a
        restore, so each is replaced on its next acquire. ASGI only."""
        async def expire() -> None:
            await pool.expire_connections()
        self.on_before_checkpoint(expire)
        self.on_after_restore(expire)

    # Running ------------------------------------------------------------

    async def run_async(self, kind: str) -> None:
        """Wait for one kind's callbacks on the event loop, at most one budget.

        A run stays registered until its callbacks finish, even after a
        delivery stops waiting, so a retried delivery waits for that same run
        instead of starting the callbacks again beside it. Coroutine callbacks
        are cancelled at the budget; a synchronous one cannot be, and the run
        lasts until its thread returns."""
        loop = asyncio.get_running_loop()
        task = self._async_runs.get(kind)
        if task is None or task.get_loop() is not loop or task.done():
            task = loop.create_task(self._execute_async(list(self._callbacks[kind])))
            self._async_runs[kind] = task

            def forget(finished: asyncio.Future[None], kind: str = kind) -> None:
                if self._async_runs.get(kind) is finished:
                    del self._async_runs[kind]
                if not finished.cancelled():
                    finished.exception()  # retrieved: callers may have stopped waiting

            task.add_done_callback(forget)
        try:
            await asyncio.wait_for(asyncio.shield(task), self.budget_seconds)
        except asyncio.TimeoutError:
            raise LifecycleHookError("lifecycle callbacks exceeded their budget") from None

    async def _execute_async(self, callbacks: list[Callback]) -> None:
        deadline = asyncio.get_running_loop().time() + self.budget_seconds
        for callback in callbacks:
            remaining = deadline - asyncio.get_running_loop().time()
            if remaining <= 0:
                raise LifecycleHookError("lifecycle callbacks exceeded their budget")
            if inspect.iscoroutinefunction(callback):
                try:
                    await asyncio.wait_for(callback(), remaining)
                except asyncio.TimeoutError:
                    raise LifecycleHookError("lifecycle callbacks exceeded their budget") from None
                continue
            result = await asyncio.to_thread(callback)
            if inspect.isawaitable(result):
                await result

    def run_sync(self, kind: str) -> None:
        """Wait for one kind's callbacks on a worker thread, at most one budget.

        A run stays registered until its thread finishes, so a retried delivery
        waits for that same run instead of starting the callbacks again beside
        it. Coroutine callbacks are refused: use the ASGI wrapper."""
        with self._lock:
            shared = self._sync_runs.get(kind)
            if shared is None:
                shared = (threading.Event(), [])
                self._sync_runs[kind] = shared
                start = True
            else:
                start = False
        done, errors = shared
        if start:
            callbacks = list(self._callbacks[kind])

            def work() -> None:
                try:
                    for callback in callbacks:
                        if inspect.iscoroutinefunction(callback):
                            raise LifecycleHookError("coroutine callbacks need the ASGI wrapper")
                        callback()
                except BaseException as error:  # noqa: BLE001 - reported to the caller
                    errors.append(error)
                finally:
                    with self._lock:
                        if self._sync_runs.get(kind) is shared:
                            del self._sync_runs[kind]
                    done.set()

            threading.Thread(target=work, name=f"gregale-{kind}", daemon=True).start()
        if not done.wait(self.budget_seconds):
            raise LifecycleHookError("lifecycle callbacks exceeded their budget")
        if errors:
            raise LifecycleHookError("lifecycle callback failed") from errors[0]

    # Middleware ---------------------------------------------------------

    def asgi(self, app: Any) -> Any:
        """Wrap an ASGI app: answer the hook paths, pass everything else through."""
        async def wrapped(scope: dict[str, Any], receive: Any, send: Any) -> None:
            kind = self.paths.get(scope.get("path", "")) if scope.get("type") == "http" else None
            if kind is None:
                await app(scope, receive, send)
                return
            headers = {k.decode("latin-1").lower(): v.decode("latin-1") for k, v in scope.get("headers", [])}
            client = scope.get("client") or ("", 0)
            status = self._admit(scope.get("method", ""), headers.get(_MARKERS[kind]), client[0])
            if status is None:
                try:
                    await self.run_async(kind)
                    status = 204
                except Exception:  # noqa: BLE001 - the platform only needs the status
                    status = 500
            await _asgi_respond(send, status)
        return wrapped

    def wsgi(self, app: Any) -> Any:
        """Wrap a WSGI app: answer the hook paths, pass everything else through."""
        def wrapped(environ: dict[str, Any], start_response: Any) -> Any:
            kind = self.paths.get(environ.get("PATH_INFO", ""))
            if kind is None:
                return app(environ, start_response)
            marker = environ.get("HTTP_" + _MARKERS[kind].upper().replace("-", "_"))
            status = self._admit(environ.get("REQUEST_METHOD", ""), marker, environ.get("REMOTE_ADDR", ""))
            if status is None:
                try:
                    self.run_sync(kind)
                    status = 204
                except Exception:  # noqa: BLE001
                    status = 500
            reason = {204: "No Content", 403: "Forbidden", 405: "Method Not Allowed", 500: "Internal Server Error"}[status]
            headers = [("Allow", "POST")] if status == 405 else []
            start_response(f"{status} {reason}", headers + [("Content-Length", "0")])
            return [b""]
        return wrapped

    @staticmethod
    def _admit(method: str, marker: str | None, peer: str) -> int | None:
        if method != "POST":
            return 405
        # Gregale strips inbound x-faas-* headers at the public proxy; the
        # loopback check also refuses a caller inside the app's network.
        if marker != "1" or not _is_loopback(peer):
            return 403
        return None


async def _asgi_respond(send: Any, status: int) -> None:
    headers = [(b"content-length", b"0")]
    if status == 405:
        headers.append((b"allow", b"POST"))
    await send({"type": "http.response.start", "status": status, "headers": headers})
    await send({"type": "http.response.body", "body": b""})


def _is_loopback(peer: str) -> bool:
    try:
        address = ipaddress.ip_address(peer)
    except ValueError:
        return False
    mapped = getattr(address, "ipv4_mapped", None)
    return (mapped or address).is_loopback
