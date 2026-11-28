"""Payload-free observations of expired retained positions; constant-memory counters."""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Literal

from .realtime_catch_up import _sequence
from .realtime_connection_status import RealtimeConnectionStatusTracker


@dataclass(frozen=True, kw_only=True)
class RealtimeMessageExpiry:
    channel: str | None = None
    inbox: bool = False
    consumer: str | None = None
    sequence: int
    transport: Literal["websocket", "sse"]
    code: Literal["message_expired"] = "message_expired"


@dataclass(frozen=True, kw_only=True)
class RealtimeExpiryStatus:
    websocket_checkpoints: int
    sse_checkpoints: int
    inbox_checkpoints: int = 0


class RealtimeExpiryDiagnostics:
    """Counts observations, including repeats after reconnect; never stores identifiers."""

    def __init__(self) -> None:
        self._websocket = self._sse = self._inbox = 0

    def record(self, event: RealtimeMessageExpiry) -> None:
        if not isinstance(event, RealtimeMessageExpiry) or event.code != "message_expired":
            raise ValueError("Invalid expiry observation")
        _sequence(event.sequence)
        if event.sequence == 0 or event.transport not in ("websocket", "sse"):
            raise ValueError("Invalid expiry observation")
        if type(event.inbox) is not bool:
            raise ValueError("Invalid expiry stream")
        if event.inbox:
            if (
                event.transport != "websocket"
                or event.channel is not None
                or not isinstance(event.consumer, str)
                or not event.consumer
            ):
                raise ValueError("Invalid inbox expiry stream")
            self._inbox = min(self._inbox + 1, 2**63 - 1)
        elif event.transport == "websocket":
            self._websocket = min(self._websocket + 1, 2**63 - 1)
        else:
            self._sse = min(self._sse + 1, 2**63 - 1)

    def snapshot(self) -> RealtimeExpiryStatus:
        return RealtimeExpiryStatus(
            websocket_checkpoints=self._websocket, sse_checkpoints=self._sse, inbox_checkpoints=self._inbox
        )


def _notify_expiry(observer: Callable[[RealtimeMessageExpiry], None] | None, event: RealtimeMessageExpiry) -> None:
    if observer is not None:
        RealtimeConnectionStatusTracker._callback(observer, event)
