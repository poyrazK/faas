"""Synchronous PostgreSQL pool adoption without a database-driver dependency."""

import re
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from typing import TypeVar
from urllib.parse import parse_qsl, unquote, urlsplit

from ._binding_client_runtime import _Cancellation, _ClientBinding, _ClientLease

T = TypeVar("T")


class PostgresPoolBindingError(Exception):
    """Sanitized adoption/retirement error without driver diagnostics."""


@dataclass(frozen=True)
class PostgresPoolConfig:
    """Explicit connection identity; never use ambient credential fallbacks."""

    connection_string: str = field(repr=False)


def _configuration(key: str, values: Mapping[str, str]) -> PostgresPoolConfig:
    value = values.get(key)
    invalid = "Postgres binding connection URI is incomplete or invalid"
    if (not isinstance(value, str) or not value or re.search(r"[\x00-\x20\x7f#]", value)
            or re.search(r"%(?![0-9a-fA-F]{2})", value)):
        raise PostgresPoolBindingError(invalid)
    try:
        url = urlsplit(value)
        if (url.scheme not in ("postgres", "postgresql") or not url.hostname or re.search(r"[%/\\\x00\r\n\t ]", url.hostname)
                or not url.username or not url.password
                or not url.path or url.path == "/" or url.netloc.endswith(":")
                or url.port is None or not 1 <= url.port <= 65535
                or re.search(r"[\x00-\x1f\x7f]", unquote(url.username + url.password + url.path))):
            raise ValueError
        if any(name.lower() in {"host", "hostaddr", "port", "user", "username", "password", "dbname", "database", "service",
                                "servicefile", "passfile", "connectionstring", "connection_string", "dsn"}
               for name, _ in parse_qsl(url.query, keep_blank_values=True)):
            raise ValueError
    except ValueError:
        raise PostgresPoolBindingError(invalid) from None
    return PostgresPoolConfig(value)


class PostgresPoolLease(_ClientLease[T]):
    """Pin a pool through a whole transaction. Return the driver's connection
    before release/context exit. Copies share idempotent release state.
    """

    def __repr__(self) -> str:
        return "PostgresPoolLease(redacted)"

    def current(self) -> T:
        return self._current()[0]


class PostgresPoolBinding(_ClientBinding[T, PostgresPoolConfig, PostgresPoolBindingError]):
    """Build and validate a fresh owned pool before publication and applied ACK.

    Register secret_consumer() with SecretReloadCoordinator. Failed adoption
    disables new acquisitions; outstanding leases can finish. Validation must
    connect, check readiness, and return its connection before returning None.
    Hooks are synchronous; bound driver timeouts and observe the cancellation
    event. Async factories/validators are rejected. Retirement runs once on a
    separate thread after the final lease, may overlap for different pools, and
    must be thread-safe and bounded. Cleanup failures are sticky and sanitized.
    Leases neither extend server credential validity nor replay transactions.
    """

    def __init__(self, key: str, factory: Callable[[PostgresPoolConfig, _Cancellation | None], T], *,
                 validate: Callable[[T, _Cancellation | None], None], retire: Callable[[T], None],
                 on_retire_error: Callable[[PostgresPoolBindingError], None] | None = None):
        if (not isinstance(key, str) or not re.fullmatch(r"[A-Z][A-Z0-9_]*", key) or not callable(factory) or not callable(validate)
                or not callable(retire) or on_retire_error is not None and not callable(on_retire_error)):
            raise PostgresPoolBindingError("Invalid postgres pool binding")
        super().__init__(lambda values: _configuration(key, values), factory, lambda _config: "",
                         lambda message: PostgresPoolBindingError(f"Postgres {message.replace('client', 'pool')}"),
                         validate=validate, retire=retire, on_retire_error=on_retire_error)

    def acquire(self) -> PostgresPoolLease[T]:
        return PostgresPoolLease(self._acquire())
