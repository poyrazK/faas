"""Immutable exact-match metadata filters and cursor/subscription identity binding."""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Mapping
from dataclasses import dataclass, field
from types import MappingProxyType


@dataclass(frozen=True, init=False)
class RealtimeEventFilter:
    """Match all supplied metadata pairs; empty filters match every event.

    Fingerprints are public configuration identities, not secrets or credentials.
    Store fingerprint alongside a locally persisted filtered processing cursor.
    """

    _entries: tuple[tuple[str, str], ...] = field(repr=False)
    fingerprint: str

    def __init__(self, values: Mapping[str, str]):
        if not isinstance(values, Mapping) or len(values) > 8:
            raise ValueError("Filters allow at most eight metadata entries")
        copied = dict(values)
        for key, value in copied.items():
            if (
                not isinstance(key, str)
                or re.fullmatch(r"[a-z0-9_.-]{1,64}", key) is None
                or not isinstance(value, str)
                or len(value.encode()) > 128
                or any(c in value for c in "\0\r\n")
            ):
                raise ValueError("Invalid realtime metadata filter")
        encoded = json.dumps(copied, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        if len(encoded) > 4096:
            raise ValueError("Filter exceeds 4096 UTF-8 bytes")
        object.__setattr__(self, "_entries", tuple(sorted(copied.items())))
        object.__setattr__(self, "fingerprint", hashlib.sha256(encoded).hexdigest())

    @property
    def values(self) -> Mapping[str, str]:
        return MappingProxyType(dict(self._entries))

    def scoped_subscription(self, name: str) -> str:
        """Derive a durable name bound to this filter; empty preserves old names."""
        from .realtime_websocket import _durable_subscription

        _durable_subscription(name)
        if name is None:
            raise ValueError("A durable subscription name is required")
        if not self._entries:
            return name
        encoded = json.dumps([name, self.fingerprint], ensure_ascii=False, separators=(",", ":")).encode()
        return "pyfilter." + hashlib.sha256(encoded).hexdigest()


def _filter_fingerprint(value: str | None) -> None:
    if value is not None and (not isinstance(value, str) or re.fullmatch(r"[0-9a-f]{64}", value) is None):
        raise ValueError("Cursor filter fingerprint must be a SHA-256 hex identity")
