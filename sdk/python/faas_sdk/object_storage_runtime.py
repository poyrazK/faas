"""Managed compute binding loader; factories supply the application's S3 library."""

import re
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from threading import RLock
from typing import Generic, TypeVar
from urllib.parse import urlsplit

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


class ObjectStorageBinding(Generic[T]):
    """Reload one atomic {revision,secrets} envelope, then ACK only after success
    and all other consumers apply. Reacquire current() for new requests; callers
    own retired clients. Failure disables acquisition, including revocation.
    Factories must use explicit credentials and configure path-style addressing.
    """

    def __init__(self, prefix: str, factory: Callable[[ObjectStorageClientConfig], T]):
        if not re.fullmatch(r"[A-Z][A-Z0-9_]{0,47}", prefix) or not callable(factory):
            raise ObjectStorageBindingError("Invalid object-storage binding")
        self._prefix = prefix
        self._factory = factory
        self._lock = RLock()
        self._active: tuple[T, str] | None = None

    def reload(self, values: Mapping[str, str]) -> None:
        with self._lock:
            self._active = None
            config = _configuration(self._prefix, values)
            try:
                client = self._factory(config)
            except Exception:
                raise ObjectStorageBindingError("Object-storage client could not be configured") from None
            if client is None:
                raise ObjectStorageBindingError("Object-storage client is unavailable")
            self._active = client, config.bucket

    def current(self) -> tuple[T, str]:
        with self._lock:
            if self._active is None:
                raise ObjectStorageBindingError("Object-storage binding is not ready")
            return self._active
