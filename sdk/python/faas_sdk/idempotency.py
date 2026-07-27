"""faas_sdk.idempotency - Idempotency-Key auto-mint.

Public API mirrors the Go SDK's `faas.WithIdempotencyKey` and the
Node SDK's `withIdempotencyKey`. The contract:

* Every mutating call (POST/PUT/PATCH/DELETE) carries an
  `Idempotency-Key` header. The SDK auto-mints a UUIDv4 if the
  caller does not supply one.
* The server replays the same response for the same key within
  24 h, so retries are safe on the client side.
* To pin a stable key for retries (CI deploys, etc.), use the
  `with_idempotency_key(key)` context manager; the auto-mint is
  suppressed for the duration of the `with` block.

The auto-mint lives in `IdempotencyTransport` (httpx
`BaseTransport`), keyed by a `ContextVar` so calls under the same
task all see the same key - and so a fresh key is minted per new
request that did not opt-in.
"""

from __future__ import annotations

import uuid
from contextvars import ContextVar

#: The HTTP header used by the daemon and the recorded replay
#: contract. See `pkg/apid/server.go::idempotent` (24 h window).
HEADER_NAME = "Idempotency-Key"

#: Type alias for caller-supplied keys. UUIDv4 is the canonical
#: shape, but any string is accepted (CI deploys pin
#: `<run>-<slug>` for traceability).
IdempotencyKey = str

#: Context-local key. None = no explicit key; auto-mint fires.
_idempotency_key: ContextVar[IdempotencyKey | None] = ContextVar(
    "faas_sdk_idempotency_key", default=None
)


def mint_idempotency_key() -> IdempotencyKey:
    """Return a fresh UUIDv4 string. Used by the auto-mint path."""
    return str(uuid.uuid4())


def current_idempotency_key() -> IdempotencyKey | None:
    """Read the current context-local key (None when no opt-in)."""
    return _idempotency_key.get()


class _IdempotencyToken:
    """Context-manager returned by `with_idempotency_key()`. Binds
    the key to the surrounding `with` block via a `ContextVar` set,
    which `IdempotencyTransport.handle_request` consults on every
    mutating call.
    """

    __slots__ = ("_key", "_token")

    def __init__(self, key: IdempotencyKey) -> None:
        self._key = key
        self._token: object | None = None

    def __enter__(self) -> IdempotencyKey:
        self._token = _idempotency_key.set(self._key)
        return self._key

    def __exit__(self, exc_type, exc, tb) -> None:
        if self._token is not None:
            _idempotency_key.reset(self._token)  # type: ignore[arg-type]
            self._token = None


def with_idempotency_key(key: IdempotencyKey) -> _IdempotencyToken:
    """Return a token that scopes `key` to a `with` block.

    Usage::

        from faas_sdk import with_idempotency_key
        from faas_sdk.api.apps import create_app

        with with_idempotency_key("deploy-2026-07-27-abc"):
            create_app.sync(client=client, slug="hello", source_url="...")

    Mirrors the Go SDK's `faas.WithIdempotencyKey(ctx, key)` and
    the Node SDK's `withIdempotencyKey(key)` AsyncLocalStorage
    shape.
    """
    return _IdempotencyToken(key)


__all__ = [
    "HEADER_NAME",
    "IdempotencyKey",
    "mint_idempotency_key",
    "current_idempotency_key",
    "with_idempotency_key",
]
