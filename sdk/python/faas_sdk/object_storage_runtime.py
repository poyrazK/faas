"""Managed compute binding loader; factories supply the application's S3 library."""

import re
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from typing import TypeVar
from urllib.parse import urlsplit

from ._binding_client_runtime import _ClientBinding, _ClientLease

T = TypeVar("T")

class ObjectStorageBindingError(Exception):
    """Sanitized configuration error; never includes driver diagnostics."""


@dataclass(frozen=True)
class ObjectStorageClientConfig:
    endpoint: str
    region: str
    bucket: str
    access_key_id: str = field(repr=False)
    secret_access_key: str = field(repr=False)
    addressing_style: str = "path"
    request_checksum_calculation: str = "when_required"
    response_checksum_validation: str = "when_required"


def _configuration(prefix: str, values: Mapping[str, str]) -> ObjectStorageClientConfig:
    def read(suffix: str) -> str:
        value = values.get(f"{prefix}_{suffix}")
        if not isinstance(value, str) or not value or re.search(r"[\x00\r\n]", value):
            raise ObjectStorageBindingError("Object-storage binding environment is incomplete or invalid")
        return value

    endpoint, region, bucket = read("ENDPOINT"), read("REGION"), read("BUCKET")
    access, secret, style = read("ACCESS_KEY_ID"), read("SECRET_ACCESS_KEY"), read("ADDRESSING_STYLE")
    try:
        url = urlsplit(endpoint)
        valid = (
            url.hostname
            and url.scheme in ("http", "https")
            and not (url.username or url.password or url.query or url.fragment)
        )
    except ValueError:
        valid = False
    if not valid or style != "path" or re.search(r"[/\\ \t?#]", region + bucket):
        raise ObjectStorageBindingError("Object-storage binding configuration is invalid")
    return ObjectStorageClientConfig(endpoint, region, bucket, access, secret)


class ObjectStorageLease(_ClientLease[T]):
    """Pin a client through stream closure. Release is idempotent across copies."""

    def __repr__(self) -> str:
        return "ObjectStorageLease(redacted)"

    def current(self) -> tuple[T, str]:
        return self._current()


class ObjectStorageBinding(_ClientBinding[T, ObjectStorageClientConfig, ObjectStorageBindingError]):
    """Adopt atomic snapshots; register secret_consumer() for coordinated ACKs.

    Hold leases through stream closure when enabling SDK-owned retirement.
    Factories must configure explicit credentials and path-style addressing.
    """

    def __init__(self, prefix: str, factory: Callable[[ObjectStorageClientConfig], T], *,
                 retire: Callable[[T], None] | None = None,
                 on_retire_error: Callable[[ObjectStorageBindingError], None] | None = None):
        if (not re.fullmatch(r"[A-Z][A-Z0-9_]{0,47}", prefix) or not callable(factory)
                or retire is not None and not callable(retire)
                or on_retire_error is not None and not callable(on_retire_error)):
            raise ObjectStorageBindingError("Invalid object-storage binding")
        super().__init__(lambda values: _configuration(prefix, values), lambda config, _cancel: factory(config),
                         lambda config: config.bucket, lambda message: ObjectStorageBindingError(f"Object-storage {message}"),
                         retire=retire, on_retire_error=on_retire_error)

    def current(self) -> tuple[T, str]:
        return self._current()

    def acquire(self) -> ObjectStorageLease[T]:
        return ObjectStorageLease(self._acquire())
