"""Internal event-loop-owned resource lifecycle shared by async bindings."""

from __future__ import annotations

import asyncio
import inspect
import math
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass
from typing import Any, Generic, Self, TypeVar

from .async_secret_reload_runtime import AsyncSecretConsumer, AsyncSecretReloadCancellation

T = TypeVar("T")
C = TypeVar("C")
V = TypeVar("V")
E = TypeVar("E", bound=Exception)


@dataclass(eq=False, repr=False)
class _BindingClient(Generic[T, V]):
    value: T
    metadata: V
    leases: int = 0
    retired: bool = False
    scheduled: bool = False


@dataclass(repr=False)
class _LeaseState(Generic[T, V]):
    binding: _AsyncClientBinding[T, Any, V, Any]
    entry: _BindingClient[T, V]
    released: bool = False


class _AsyncClientLease(Generic[T, V]):
    """Hold through the entire operation, including connection/body closure.

    Async context exit releases synchronously, even when its task is cancelled. Shallow
    copies share idempotent release state. Driver objects remain sensitive.
    """

    def __init__(self, state: _LeaseState[T, V]):
        self._state = state

    def __repr__(self) -> str:
        return "_AsyncClientLease(redacted)"

    def _current(self) -> tuple[T, V]:
        self._state.binding._check_loop()
        if self._state.released:
            raise self._state.binding._error("lease is released")
        return self._state.entry.value, self._state.entry.metadata

    def release(self) -> None:
        state = self._state
        state.binding._check_loop()
        if not state.released:
            state.released = True
            state.entry.leases -= 1
            state.binding._schedule_retirement(state.entry)

    async def __aenter__(self) -> Self:
        self._current()
        return self

    async def __aexit__(self, *_exc: object) -> None:
        self.release()


class _AsyncClientBinding(Generic[T, C, V, E]):
    """Create, validate, lease and retire fresh clients on one event loop.

    All three lifecycle hooks must return awaitables. Factories own partial
    allocations until they return a client. Invalidation immediately fences new
    acquisitions and late publication; existing leases finish. Cancellation
    returns promptly, while owned work/cleanup drains in the background. Driver
    hooks must cooperate with cancellation and bound their own I/O. No replay.
    """

    def __init__(self, load: Callable[[Mapping[str, str]], C],
                 factory: Callable[[C, AsyncSecretReloadCancellation], Awaitable[T]],
                 value: Callable[[C], V], error: Callable[[str], E], *,
                 validate: Callable[..., Awaitable[None]], retire: Callable[[T], Awaitable[None]],
                 on_retire_error: Callable[[E], None] | None = None):
        self._load, self._factory, self._value, self._error = load, factory, value, error
        self._validate, self._retire = validate, retire
        self._on_retire_error = on_retire_error
        self._loop: asyncio.AbstractEventLoop | None = None
        self._reloads = asyncio.Lock()
        self._epoch = 0
        self._active: _BindingClient[T, V] | None = None
        self._retired: set[_BindingClient[T, V]] = set()
        self._tasks: set[asyncio.Task] = set()
        self._building = 0
        self._changed = asyncio.Event()
        self._retirement_failed = False

    def _check_loop(self) -> None:
        loop = asyncio.get_running_loop()
        if self._loop is None:
            self._loop = loop
        if self._loop is not loop:
            raise self._error("client belongs to another event loop")

    def _track(self, task: asyncio.Task) -> None:
        self._tasks.add(task)
        task.add_done_callback(self._tasks.discard)
        task.add_done_callback(lambda done: None if done.cancelled() else done.exception())

    async def reload(self, values: Mapping[str, str], cancel: AsyncSecretReloadCancellation | None = None) -> None:
        self._check_loop()
        if cancel is not None and not isinstance(cancel, AsyncSecretReloadCancellation):
            raise self._error("async cancellation is invalid")
        snapshot, epoch = dict(values), self._epoch
        local = asyncio.Event()
        # Link rather than copy the caller token so deadlines stay observable.
        token = (cancel or AsyncSecretReloadCancellation()).with_event(local)
        if token.is_set():
            raise asyncio.CancelledError
        task = asyncio.create_task(self._reload(snapshot, epoch, token, local))
        self._track(task)
        watcher = asyncio.create_task(token.wait())
        try:
            await asyncio.wait((task, watcher), return_when=asyncio.FIRST_COMPLETED)
            token.check()
            if not task.done():
                raise self._error("cancellation could not be observed")
            return task.result()
        finally:
            local.set()
            watcher.cancel()
            if not task.done() and not task.cancelling():
                task.cancel()
            await asyncio.gather(watcher, return_exceptions=True)

    async def _reload(self, values: Mapping[str, str], epoch: int, token: AsyncSecretReloadCancellation,
                      local: asyncio.Event) -> None:
        def check() -> None:
            token.check()
            if local.is_set() or epoch != self._epoch:
                raise asyncio.CancelledError

        async with self._reloads:
            check()
            self._building += 1
            self._clear_active()
            candidate = None
            published = False
            try:
                config = self._load(values)
                metadata = self._value(config)
                check()
                result = self._factory(config, token)
                valid_factory = inspect.isawaitable(result)
                client = await result if valid_factory else result
                if client is None:
                    raise self._error("client is unavailable")
                if any(entry.value is client for entry in self._retired):
                    raise self._error("factory reused an owned client")
                candidate = _BindingClient(client, metadata)
                if not valid_factory:
                    raise self._error("factory must be async")
                check()
                result = await self._validate_client(client, config, token)
                if result is not None:
                    if inspect.iscoroutine(result):
                        result.close()
                    raise self._error("client validation failed")
                check()
                self._active = candidate
                published = True
            except asyncio.CancelledError:
                raise asyncio.CancelledError from None
            except BaseException:
                raise self._error("client could not be adopted") from None
            finally:
                if candidate is not None and not published:
                    self._retire_client(candidate)
                self._building -= 1
                self._changed.set()

    async def _validate_client(self, client: T, config: C, cancel: AsyncSecretReloadCancellation) -> None:
        return await self._validate(client, cancel)

    def invalidate(self) -> None:
        """Immediately fence queued/in-flight work; preserve outstanding leases."""
        self._check_loop()
        self._epoch += 1
        self._clear_active()

    def secret_consumer(self) -> AsyncSecretConsumer:
        async def apply(snapshot, cancel):
            await self.reload(snapshot.secrets, cancel)
        return AsyncSecretConsumer(apply, self.invalidate)

    def _acquire(self) -> _LeaseState[T, V]:
        self._check_loop()
        if self._active is None:
            raise self._error("binding is not ready")
        self._active.leases += 1
        return _LeaseState(self, self._active)

    def _clear_active(self) -> None:
        if self._active is not None:
            old, self._active = self._active, None
            self._retire_client(old)

    def _retire_client(self, entry: _BindingClient[T, V]) -> None:
        if not entry.retired:
            entry.retired = True
            self._retired.add(entry)
            self._schedule_retirement(entry)
            self._changed.set()

    def _schedule_retirement(self, entry: _BindingClient[T, V]) -> None:
        if entry.retired and not entry.leases and not entry.scheduled:
            entry.scheduled = True
            self._track(asyncio.create_task(self._dispose(entry)))

    async def _dispose(self, entry: _BindingClient[T, V]) -> None:
        failed = False
        try:
            result = await self._retire(entry.value)
            failed = result is not None
            if inspect.iscoroutine(result):
                result.close()
        except BaseException:
            failed = True
        self._retired.remove(entry)
        self._retirement_failed |= failed
        self._changed.set()
        if failed and self._on_retire_error is not None:
            try:
                result = self._on_retire_error(self._error("client retirement failed"))
                if inspect.iscoroutine(result):
                    result.close()
            except BaseException:
                pass

    async def wait_for_retirement(self, timeout: float | None = None) -> None:
        """Drain admitted work and retired leases/cleanup; ignore active clients.

        Stop admission/reloads and invalidate first for shutdown. Cancellation or
        timeout ends only this wait. Cleanup errors are sticky, without retries.
        """
        self._check_loop()
        if timeout is not None and (not isinstance(timeout, (float, int)) or not math.isfinite(timeout) or timeout < 0):
            raise self._error("invalid retirement timeout")
        try:
            async with asyncio.timeout(timeout):
                while self._building or self._retired:
                    self._changed.clear()
                    await self._changed.wait()
            if self._retirement_failed:
                raise self._error("client retirement failed")
        except TimeoutError:
            raise self._error("retirement wait timed out") from None
