# Generated from sdk/python; run python sdk/python/scripts/sync_binding_starters.py.
"""Driver-independent async object-storage adoption and streaming leases."""

import re
from collections.abc import Awaitable, Callable
from typing import TypeVar

from ._async_binding_client_runtime import _AsyncClientBinding, _AsyncClientLease
from .async_secret_reload_runtime import AsyncSecretReloadCancellation
from .object_storage_runtime import ObjectStorageBindingError, ObjectStorageClientConfig, _configuration

T = TypeVar("T")


class AsyncObjectStorageLease(_AsyncClientLease[T, str]):
    """Pin a client through upload/download body closure. Async context exit and
    release are idempotent across shallow copies; close bodies before release.
    """

    def __repr__(self) -> str:
        return "AsyncObjectStorageLease(redacted)"

    def current(self) -> tuple[T, str]:
        return self._current()


class AsyncObjectStorageBinding(_AsyncClientBinding[T, ObjectStorageClientConfig, str, ObjectStorageBindingError]):
    """Adopt explicit credentials on one event loop, without importing a driver.

    Required async factory/validator/retirement hooks own fresh clients. The
    validator receives (client, bucket, cancel), performs an application-chosen
    readiness check and closes its response body before returning None. Retire
    returns None after cleanup. Failed adoption disables new leases; existing
    streams retain their client until release. Partial allocations remain the
    factory's responsibility until return. Bound all I/O and drain before loop
    shutdown. Leases neither extend credential validity nor retry requests.
    """

    def __init__(self, prefix: str,
                 factory: Callable[[ObjectStorageClientConfig, AsyncSecretReloadCancellation], Awaitable[T]], *,
                 validate: Callable[[T, str, AsyncSecretReloadCancellation], Awaitable[None]],
                 retire: Callable[[T], Awaitable[None]],
                 on_retire_error: Callable[[ObjectStorageBindingError], None] | None = None):
        if (not isinstance(prefix, str) or not re.fullmatch(r"[A-Z][A-Z0-9_]{0,47}", prefix)
                or not all(callable(hook) for hook in (factory, validate, retire))
                or on_retire_error is not None and not callable(on_retire_error)):
            raise ObjectStorageBindingError("Invalid async object-storage binding")
        super().__init__(lambda values: _configuration(prefix, values), factory, lambda config: config.bucket,
                         lambda message: ObjectStorageBindingError(f"Object-storage {message}"),
                         validate=validate, retire=retire, on_retire_error=on_retire_error)

    async def _validate_client(self, client: T, config: ObjectStorageClientConfig,
                               cancel: AsyncSecretReloadCancellation) -> None:
        return await self._validate(client, config.bucket, cancel)

    def acquire(self) -> AsyncObjectStorageLease[T]:
        return AsyncObjectStorageLease(self._acquire())
