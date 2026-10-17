# Generated from sdk/python; run python sdk/python/scripts/sync_binding_starters.py.
"""Internal owned-resource lifecycle shared by runtime bindings."""

import inspect
import math
import time
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from threading import Condition, Lock, RLock, Thread
from typing import TYPE_CHECKING, Any, Generic, Protocol, Self, TypeVar

T = TypeVar("T")
C = TypeVar("C")
E = TypeVar("E", bound=Exception)
if TYPE_CHECKING:
    from .secret_reload_runtime import SecretConsumer

class _Cancellation(Protocol):
    def is_set(self) -> bool: ...

@dataclass(eq=False, repr=False)
class _BindingClient(Generic[T]):
    pair: tuple[T, str]
    leases: int = 0
    retired: bool = False
    scheduled: bool = False


@dataclass(repr=False)
class _LeaseState(Generic[T]):
    binding: "_ClientBinding[T, Any, Any]"
    entry: _BindingClient[T]
    released: bool = False


class _ClientLease(Generic[T]):
    """Pin a client until release, including response-body consumption/closure.

    Release is idempotent even across shallow copies. Context-manager exit
    releases the lease; close application response bodies before that exit.
    """

    def __init__(self, state: _LeaseState[T]):
        self._state = state

    def __repr__(self) -> str:
        return "_ClientLease(redacted)"

    def _current(self) -> tuple[T, str]:
        with self._state.binding._lock:
            if self._state.released:
                raise self._state.binding._error("lease is released")
            return self._state.entry.pair

    def release(self) -> None:
        state = self._state
        with state.binding._lock:
            if state.released:
                return
            state.released = True
            state.entry.leases -= 1
            state.binding._schedule_retirement(state.entry)

    def __enter__(self) -> "Self":
        self._current()
        return self

    def __exit__(self, *_exc: object) -> None:
        self.release()


class _ClientBinding(Generic[T, C, E]):
    """Reload one atomic {revision,secrets} envelope, then ACK only after success
    and all other consumers apply. Acquire a lease through stream closure.
    Without a retire callback, current() retains caller-owned behavior.
    Failure disables new acquisition, including revocation.
    Factories must create fresh, exclusively owned resources.
    """

    def __init__(self, load: Callable[[Mapping[str, str]], C],
                 factory: Callable[[C, _Cancellation | None], T], value: Callable[[C], str],
                 error: Callable[[str], E], *,
                 validate: Callable[[T, _Cancellation | None], None] | None = None,
                 retire: Callable[[T], None] | None = None,
                 on_retire_error: Callable[[E], None] | None = None):
        self._load, self._factory, self._value = load, factory, value
        self._error, self._validate = error, validate
        self._lock = RLock()
        self._reloads = Lock()
        self._epoch = 0
        self._active: _BindingClient[T] | None = None
        self._retired: set[_BindingClient[T]] = set()
        self._building = 0
        self._condition = Condition(self._lock)
        self._retire = retire
        self._on_retire_error = on_retire_error
        self._retirement_failed = False

    def reload(self, values: Mapping[str, str], cancel: _Cancellation | None = None) -> None:
        # Preserve the caller's factory thread while keeping state acquisition
        # and invalidation independent of construction/readiness latency.
        snapshot = dict(values)
        with self._lock:
            epoch = self._epoch
        while True:
            if cancel is not None and cancel.is_set():
                raise self._error("reload cancelled")
            if self._reloads.acquire(timeout=0.05):
                break
        admitted = False
        try:
            with self._lock:
                if epoch != self._epoch or cancel is not None and cancel.is_set():
                    raise self._error("reload cancelled")
                self._building += 1
                admitted = True
                self._clear_active()
            config = self._load(snapshot)
            if cancel is not None and cancel.is_set():
                raise self._error("reload cancelled")
            try:
                client = self._factory(config, cancel)
                if inspect.isawaitable(client):
                    if hasattr(client, "close"):
                        client.close()
                    raise self._error("client could not be configured")
            except BaseException:
                raise self._error("client could not be configured") from None
            if client is None:
                raise self._error("client is unavailable")
            with self._lock:
                if self._retire is not None and any(entry.pair[0] is client for entry in self._retired):
                    raise self._error("factory reused an owned client")
                candidate = _BindingClient((client, self._value(config)))
            published = False
            try:
                with self._lock:
                    if epoch != self._epoch or cancel is not None and cancel.is_set():
                        raise self._error("reload cancelled")
                if self._validate is not None:
                    try:
                        result = self._validate(client, cancel)
                        if result is not None:
                            if hasattr(result, "close"):
                                result.close()
                            raise self._error("client validation failed")
                    except BaseException:
                        raise self._error("client validation failed") from None
                with self._lock:
                    if epoch != self._epoch or cancel is not None and cancel.is_set():
                        raise self._error("reload cancelled")
                    self._active = candidate
                    published = True
            finally:
                if not published:
                    with self._lock:
                        self._retire_client(candidate)
        finally:
            if admitted:
                with self._lock:
                    self._building -= 1
                    self._condition.notify_all()
            self._reloads.release()

    def invalidate(self) -> None:
        """Fence new/queued acquisition; keep clients with outstanding leases."""
        with self._lock:
            self._epoch += 1
            self._clear_active()

    def secret_consumer(self) -> "SecretConsumer":
        from .secret_reload_runtime import SecretConsumer

        return SecretConsumer(lambda snapshot, cancel: self.reload(snapshot.secrets, cancel), self.invalidate)

    def _current(self) -> tuple[T, str]:
        with self._lock:
            if self._active is None:
                raise self._error("binding is not ready")
            if self._retire is not None:
                raise self._error("binding requires lease acquisition for managed retirement")
            return self._active.pair

    def _acquire(self) -> _LeaseState[T]:
        with self._lock:
            if self._active is None:
                raise self._error("binding is not ready")
            self._active.leases += 1
            return _LeaseState(self, self._active)

    def wait_for_retirement(self, timeout: float | None = None, cancel: _Cancellation | None = None) -> None:
        """Drain admitted factories and retired leases/cleanup. Ignores active.

        Stop serving/reloading and invalidate first for shutdown. Timeout/cancel
        ends only this wait. Callback failures are sticky and never retried.
        """
        if timeout is not None and (not math.isfinite(timeout) or timeout < 0):
            raise self._error("invalid retirement timeout")
        deadline = None if timeout is None else time.monotonic() + timeout
        with self._condition:
            while True:
                if cancel is not None and cancel.is_set():
                    raise self._error("retirement wait cancelled")
                if self._building == 0 and not self._retired:
                    if self._retirement_failed:
                        raise self._error("client retirement failed")
                    return
                remaining = None if deadline is None else deadline - time.monotonic()
                if remaining is not None and remaining <= 0:
                    raise self._error("retirement wait timed out")
                self._condition.wait(remaining if cancel is None else min(remaining, 0.05) if remaining is not None else 0.05)

    def _clear_active(self) -> None:
        if self._active is not None:
            entry, self._active = self._active, None
            self._retire_client(entry)

    def _retire_client(self, entry: _BindingClient[T]) -> None:
        if entry.retired:
            return
        entry.retired = True
        self._retired.add(entry)
        self._schedule_retirement(entry)
        self._condition.notify_all()

    def _schedule_retirement(self, entry: _BindingClient[T]) -> None:
        if not entry.retired or entry.leases or entry.scheduled:
            return
        entry.scheduled = True
        if self._retire is None:
            self._retired.remove(entry)
            self._condition.notify_all()
            return
        Thread(target=self._dispose_client, args=(entry,), daemon=True).start()

    def _dispose_client(self, entry: _BindingClient[T]) -> None:
        failed = False
        try:
            # Run outside the lock. Factories must create exclusively owned
            # resources; synchronous hooks must be thread-safe and bound work.
            result = self._retire(entry.pair[0])  # type: ignore[misc]
            if result is not None:
                if hasattr(result, "close"):
                    result.close()
                failed = True
        except BaseException:
            failed = True
        with self._lock:
            self._retired.remove(entry)
            self._retirement_failed |= failed
            self._condition.notify_all()
        if failed and self._on_retire_error is not None:
            try:
                self._on_retire_error(self._error("client retirement failed"))
            except BaseException:
                pass  # Observers cannot poison cleanup.
