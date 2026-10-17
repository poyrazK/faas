"""Driver-independent async PostgreSQL pool adoption on one owning event loop."""

import re
from collections.abc import Awaitable, Callable
from typing import TypeVar

from ._async_binding_client_runtime import _AsyncClientBinding, _AsyncClientLease
from .async_secret_reload_runtime import AsyncSecretReloadCancellation
from .postgres_pool_runtime import PostgresPoolBindingError, PostgresPoolConfig, _configuration

T = TypeVar("T")


class AsyncPostgresPoolLease(_AsyncClientLease[T, None]):
    """Hold through transaction completion AND connection return. Copies share
    idempotent release state; driver objects remain sensitive.
    """

    def __repr__(self) -> str:
        return "AsyncPostgresPoolLease(redacted)"

    def current(self) -> T:
        return self._current()[0]


class AsyncPostgresPoolBinding(_AsyncClientBinding[T, PostgresPoolConfig, None, PostgresPoolBindingError]):
    """Create, validate, lease and retire fresh pools on one owning event loop.

    All lifecycle hooks return awaitables; validators and retire hooks return
    None. Factories own partial allocations until return. Cancellation fences
    late publication and retains ownership for cleanup. Existing leases finish.
    Bound driver I/O and drain before closing the loop. No transaction replay.
    """

    def __init__(self, key: str,
                 factory: Callable[[PostgresPoolConfig, AsyncSecretReloadCancellation], Awaitable[T]], *,
                 validate: Callable[[T, AsyncSecretReloadCancellation], Awaitable[None]],
                 retire: Callable[[T], Awaitable[None]],
                 on_retire_error: Callable[[PostgresPoolBindingError], None] | None = None):
        if (not isinstance(key, str) or not re.fullmatch(r"[A-Z][A-Z0-9_]*", key)
                or not all(callable(hook) for hook in (factory, validate, retire))
                or on_retire_error is not None and not callable(on_retire_error)):
            raise PostgresPoolBindingError("Invalid async postgres pool binding")
        super().__init__(lambda values: _configuration(key, values), factory, lambda _config: None,
                         lambda message: PostgresPoolBindingError(f"Postgres {message.replace('client', 'pool')}"),
                         validate=validate, retire=retire, on_retire_error=on_retire_error)

    def acquire(self) -> AsyncPostgresPoolLease[T]:
        return AsyncPostgresPoolLease(self._acquire())
