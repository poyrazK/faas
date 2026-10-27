"""Payload-free outcome of a retained-event graceful shutdown."""

from dataclasses import dataclass


class _RecoveryDrained(Exception):
    """Adapter finished its frozen queue, including iterator-driven ACKs."""


@dataclass(frozen=True, kw_only=True)
class RealtimeDrainResult:
    complete: bool
    processed_sequence: int
    confirmed_sequence: int | None
    final_ack_sent: bool
    deadline_expired: bool
    unfinished_event_count: int
    error_reason: str | None = None
