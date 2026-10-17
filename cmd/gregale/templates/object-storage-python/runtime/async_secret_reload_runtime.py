# Generated from sdk/python; run python sdk/python/scripts/sync_binding_starters.py.
"""Event-loop-owned secret adoption; no control-plane or provider calls."""

from __future__ import annotations

import asyncio
import inspect
import json
import math
import re
import signal
import threading
import time
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass

import httpx

from .secret_reload_runtime import SecretReloadError, SecretSnapshot, _read, _ready, _reload_settings


class AsyncSecretReloadCancellation:
    """Loop-local cancellation/deadline shared with async consumer hooks."""

    def __init__(self, events: Sequence[asyncio.Event] = (), deadline: float | None = None):
        self._events = tuple(events)
        if (any(not isinstance(event, asyncio.Event) for event in self._events)
                or deadline is not None and (not isinstance(deadline, (float, int)) or not math.isfinite(deadline))):
            raise SecretReloadError("Async secret cancellation is invalid")
        self._deadline = deadline

    def is_set(self) -> bool:
        return any(event.is_set() for event in self._events) or (
            self._deadline is not None and time.monotonic() >= self._deadline
        )

    def check(self) -> None:
        if self.is_set():
            raise asyncio.CancelledError

    def with_timeout(self, seconds: float) -> AsyncSecretReloadCancellation:
        if not isinstance(seconds, (float, int)) or not math.isfinite(seconds) or seconds < 0:
            raise SecretReloadError("Async secret cancellation deadline is invalid")
        deadline = time.monotonic() + seconds
        return AsyncSecretReloadCancellation(self._events, min(deadline, self._deadline)
                                            if self._deadline is not None else deadline)

    def with_event(self, event: asyncio.Event) -> AsyncSecretReloadCancellation:
        return AsyncSecretReloadCancellation((*self._events, event), self._deadline)

    async def wait(self, timeout: float | None = None) -> bool:
        if timeout is not None and (not isinstance(timeout, (float, int)) or not math.isfinite(timeout) or timeout < 0):
            raise SecretReloadError("Async secret cancellation wait is invalid")
        remaining = None if self._deadline is None else max(0, self._deadline - time.monotonic())
        if timeout is not None:
            remaining = timeout if remaining is None else min(timeout, remaining)
        tasks = [asyncio.create_task(event.wait()) for event in self._events]
        try:
            if tasks:
                await asyncio.wait(tasks, timeout=remaining, return_when=asyncio.FIRST_COMPLETED)
            elif remaining is not None:
                await asyncio.sleep(remaining)
            else:
                await asyncio.Future()
            return self.is_set()
        finally:
            for task in tasks:
                if not task.cancelling():
                    task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)


@dataclass(frozen=True)
class AsyncSecretConsumer:
    """Apply asynchronously; invalidate synchronously to fence publication now.

    Hooks run on the coordinator's event loop. Apply must check cancellation
    before publishing. Invalidation must be idempotent and must not await I/O.
    """

    apply: Callable[[SecretSnapshot, AsyncSecretReloadCancellation], Awaitable[None]]
    invalidate: Callable[[], None]


async def _async_read(path: str, cancel: AsyncSecretReloadCancellation) -> str:
    return await asyncio.to_thread(_read, path, cancel)


async def _async_ready(path: str, cancel: AsyncSecretReloadCancellation) -> None:
    await asyncio.to_thread(_ready, path, cancel)


async def _async_post(endpoint: str, body: bytes, cancel: AsyncSecretReloadCancellation) -> int:
    cancel.check()
    # No proxy environment, redirects, API client, or response-body parsing.
    async with httpx.AsyncClient(trust_env=False, follow_redirects=False, timeout=5) as client:
        async with client.stream("POST", endpoint, content=body, headers={"Content-Type": "application/json"}) as response:
            cancel.check()
            return response.status_code


async def _async_sleep(seconds: float, cancel: AsyncSecretReloadCancellation) -> None:
    await cancel.wait(seconds)
    cancel.check()


@dataclass(frozen=True)
class AsyncSecretReloadIO:
    """Async local adapters. Hooks must cooperate with cancellation/deadlines."""

    read: Callable[[str, AsyncSecretReloadCancellation], Awaitable[str]] = _async_read
    ready: Callable[[str, AsyncSecretReloadCancellation], Awaitable[None]] = _async_ready
    post: Callable[[str, bytes, AsyncSecretReloadCancellation], Awaitable[int]] = _async_post
    sleep: Callable[[float, AsyncSecretReloadCancellation], Awaitable[None]] = _async_sleep


def _consume(task: asyncio.Task) -> None:
    if not task.cancelled():
        task.exception()


class AsyncSecretReloadCoordinator:
    """One coordinator on one main-thread event loop for its entire lifetime.

    Await start() before serving. stop() immediately disables consumers and
    fences outstanding work; await wait_stopped() to drain owned tasks. A
    cancelled manual reload fences only its admitted attempt, preserving native
    task cancellation. Callbacks that suppress cancellation still cannot ACK.
    """

    def __init__(self, consumers: Sequence[AsyncSecretConsumer], *, env: Mapping[str, str] | None = None,
                 cancel: asyncio.Event | None = None, startup_timeout: float = 30,
                 on_error: Callable[[SecretReloadError], None] | None = None, io: AsyncSecretReloadIO | None = None):
        if not consumers or any(not callable(getattr(c, "apply", None))
                                or not callable(getattr(c, "invalidate", None)) for c in consumers):
            raise SecretReloadError("Secret consumers are unavailable")
        if cancel is not None and not isinstance(cancel, asyncio.Event):
            raise SecretReloadError("Async secret cancellation requires asyncio.Event")
        self._snapshot, self._ready, self._endpoint, self._generation = _reload_settings(env, startup_timeout)
        self._consumers, self._io = tuple(consumers), io or AsyncSecretReloadIO()
        self._startup_timeout, self._on_error = startup_timeout, on_error
        self._shutdown = asyncio.Event()
        self._external = cancel
        self._loop: asyncio.AbstractEventLoop | None = None
        self._lock = asyncio.Lock()
        self._tasks: set[asyncio.Task] = set()
        self._initial: asyncio.Task | None = None
        self._active: AsyncSecretReloadCancellation | None = None
        self._started = self._installed = self._hangup_pending = False
        self._hangup_task: asyncio.Task | None = None
        self._handler = self._on_hangup
        self._previous: object = None

    def _check_loop(self) -> None:
        loop = asyncio.get_running_loop()
        if self._loop is None:
            self._loop = loop
        if self._loop is not loop:
            raise SecretReloadError("Secret reload belongs to another event loop")

    def _track(self, task: asyncio.Task) -> asyncio.Task:
        self._tasks.add(task)
        task.add_done_callback(self._tasks.discard)
        task.add_done_callback(_consume)
        return task

    async def start(self) -> str:
        self._check_loop()
        try:
            if self._shutdown.is_set() or self._external is not None and self._external.is_set():
                raise SecretReloadError("Secret reload cancelled")
            if not self._started:
                if threading.current_thread() is not threading.main_thread() or not hasattr(signal, "SIGHUP"):
                    raise SecretReloadError("Secret reload startup requires SIGHUP on the main thread")
                self._previous = signal.getsignal(signal.SIGHUP)
                if callable(self._previous):
                    raise SecretReloadError("SIGHUP already has an application handler")
                signal.signal(signal.SIGHUP, self._handler)
                self._started = self._installed = True
                self._initial = self._track(asyncio.create_task(self._submit(True, None, self._startup_timeout)))
                if self._external is not None:
                    self._track(asyncio.create_task(self._watch_cancellation()))
            return await asyncio.shield(self._initial)
        except BaseException:
            self.stop()
            raise

    async def reload(self, *, cancel: asyncio.Event | None = None, timeout: float | None = None) -> str:
        self._check_loop()
        if not self._started or self._shutdown.is_set():
            raise SecretReloadError("Secret reload is not running")
        return await self._submit(False, cancel, timeout)

    async def _submit(self, initial: bool, cancel: asyncio.Event | None, timeout: float | None) -> str:
        if cancel is not None and not isinstance(cancel, asyncio.Event):
            raise SecretReloadError("Async secret cancellation requires asyncio.Event")
        if timeout is not None and (not isinstance(timeout, (int, float)) or not math.isfinite(timeout) or timeout < 0):
            raise SecretReloadError("Secret reload deadline is invalid")
        local = asyncio.Event()
        events = [self._shutdown, local] + ([self._external] if self._external is not None else [])
        if cancel is not None:
            events.append(cancel)
        token = AsyncSecretReloadCancellation(events)
        if timeout is not None:
            token = token.with_timeout(timeout)
        if token.is_set():
            raise SecretReloadError("Secret reload cancelled")
        task = self._track(asyncio.create_task(self._attempt(initial, token)))
        watcher = asyncio.create_task(token.wait())
        succeeded = False
        try:
            await asyncio.wait((task, watcher), return_when=asyncio.FIRST_COMPLETED)
            if token.is_set():
                raise SecretReloadError("Secret reload cancelled")
            if not task.done():
                raise SecretReloadError("Secret cancellation could not be observed")
            revision = task.result()
            succeeded = True
            return revision
        finally:
            if initial and not succeeded:
                self._shutdown.set()
            local.set()
            watcher.cancel()
            if not task.done():
                if self._active is token:
                    self._disable()
                if not task.cancelling():
                    task.cancel()
            await asyncio.gather(watcher, return_exceptions=True)

    async def _attempt(self, initial: bool, token: AsyncSecretReloadCancellation) -> str:
        async with self._lock:
            token.check()  # A cancelled queued attempt must not disable another adoption.
            self._active = token
            try:
                return await self._refresh(initial, token)
            except asyncio.CancelledError:
                if initial:
                    self._shutdown.set()
                self._disable()
                raise asyncio.CancelledError from None
            except BaseException:
                if initial:
                    self._shutdown.set()
                self._disable()
                raise SecretReloadError("Secret reload could not be completed") from None
            finally:
                self._active = None

    def _disable(self) -> bool:
        success = True
        for consumer in self._consumers:
            try:
                result = consumer.invalidate()
                if result is not None:
                    if inspect.iscoroutine(result):
                        result.close()
                    success = False
            except BaseException:
                success = False
        return success

    async def _refresh(self, initial: bool, token: AsyncSecretReloadCancellation) -> str:
        token.check()
        if not self._disable():
            raise SecretReloadError("Secret consumers could not be disabled")
        if initial:
            await self._io.ready(self._ready, token)
        for attempt in range(5):
            token.check()
            if not self._disable():
                raise SecretReloadError("Secret consumers could not be disabled")
            snapshot = await self._read_snapshot(initial and attempt == 0, token)
            status = "applied"
            try:
                for consumer in self._consumers:
                    token.check()
                    result = await consumer.apply(snapshot, token)
                    if result is not None:
                        if inspect.iscoroutine(result):
                            result.close()
                        raise SecretReloadError("Secret consumer must return None")
                    token.check()
            except asyncio.CancelledError:
                raise
            except BaseException:
                self._disable()
                token.check()
                status = "failed"
            if await self._ack(snapshot.revision, status, token):
                continue
            if status == "failed":
                raise SecretReloadError("Secret snapshot could not be applied")
            token.check()
            return snapshot.revision
        raise SecretReloadError("Secret projection changed too many times")

    async def _read_snapshot(self, initial: bool, token: AsyncSecretReloadCancellation) -> SecretSnapshot:
        backoff = 0.1
        while True:
            for attempt in range(5):
                token.check()
                try:
                    raw = await self._io.read(self._snapshot, token)
                    token.check()
                    break
                except FileNotFoundError:
                    if attempt == 4:
                        raise SecretReloadError("Secret snapshot could not be read") from None
            parsed = json.loads(raw)
            if (not isinstance(parsed, dict) or not isinstance(parsed.get("revision"), str)
                    or not (initial and parsed["revision"] == "" or re.fullmatch(r"[0-9a-f]{64}", parsed["revision"]))
                    or not isinstance(parsed.get("secrets"), dict)
                    or any(not key or not isinstance(value, str) for key, value in parsed["secrets"].items())):
                raise SecretReloadError("Secret snapshot is invalid")
            snapshot = SecretSnapshot(parsed["revision"], parsed["secrets"])
            if snapshot.revision:
                return snapshot
            await self._io.sleep(backoff, token)
            backoff = min(2, backoff * 2)

    async def _ack(self, revision: str, status: str, token: AsyncSecretReloadCancellation) -> bool:
        body = json.dumps({"revision": revision, "status": status, "generation": self._generation}).encode()
        for attempt in range(10):
            token.check()
            request = token.with_timeout(5)
            try:
                async with asyncio.timeout(5):
                    code = await self._io.post(self._endpoint, body, request)
                token.check()
                request.check()
            except (Exception, asyncio.CancelledError):
                token.check()
                # A parent task cancellation is never an ACK transport retry.
                if asyncio.current_task().cancelling():
                    raise asyncio.CancelledError
                code = None
            if code == 202:
                return False
            if code == 409:
                return True
            if code is not None and code != 503 or attempt == 9:
                break
            await self._io.sleep(min(2, 0.1 * 2**attempt), token)
        raise SecretReloadError("Secret acknowledgement could not be confirmed")

    def _on_hangup(self, *_args: object) -> None:
        self._loop.call_soon(self._queue_hangup)

    def _queue_hangup(self) -> None:
        if self._shutdown.is_set():
            return
        self._hangup_pending = True
        if self._hangup_task is None or self._hangup_task.done():
            self._hangup_task = self._track(asyncio.create_task(self._pump_hangups()))

    async def _pump_hangups(self) -> None:
        while self._hangup_pending and not self._shutdown.is_set():
            self._hangup_pending = False
            try:
                await self.reload()
            except asyncio.CancelledError:
                return
            except Exception:
                if self._on_error is not None:
                    try:
                        result = self._on_error(SecretReloadError("Secret reload could not be completed"))
                        if inspect.iscoroutine(result):
                            result.close()
                    except BaseException:
                        pass

    async def _watch_cancellation(self) -> None:
        await self._external.wait()
        self.stop()

    def stop(self) -> None:
        self._check_loop()
        self._shutdown.set()
        if self._installed:
            if signal.getsignal(signal.SIGHUP) is self._handler:
                signal.signal(signal.SIGHUP, self._previous)
            self._installed = False
        self._disable()
        current = asyncio.current_task()
        for task in tuple(self._tasks):
            if task is not current and not task.cancelling():
                task.cancel()

    async def wait_stopped(self, timeout: float | None = None) -> None:
        """Drain owned tasks after stop; a timeout never forcibly closes pools."""
        self._check_loop()
        if timeout is not None and (not isinstance(timeout, (float, int)) or not math.isfinite(timeout) or timeout < 0):
            raise SecretReloadError("Secret reload shutdown deadline is invalid")
        if not self._shutdown.is_set():
            raise SecretReloadError("Secret reload is still running")
        tasks = self._tasks - {asyncio.current_task()}
        if tasks:
            _, pending = await asyncio.wait(tasks, timeout=timeout)
            if pending:
                raise SecretReloadError("Secret reload shutdown timed out")
