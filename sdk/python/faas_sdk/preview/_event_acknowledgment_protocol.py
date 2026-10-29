"""Bounded immutable acknowledgment observations and cooperative worker control."""

from __future__ import annotations

import asyncio
import re
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any, Protocol, TypeVar

from ._event_deadline_policy import timestamp
from .event_consumer_transactions import EventConsumerTransactionResult


@dataclass(frozen=True)
class EventAcknowledgmentWorkerOptions:
    callback_attempts: int = 3
    callback_timeout_ms: int | None = None
    retry_backoff_ms: int | None = None


@dataclass(frozen=True)
class EventAcknowledgmentWorkerStatus:
    invocation_id: str
    attempt: int
    replay_generation: int
    state: str
    processing_state: str
    disposition: str | None = None
    failure_reason: str | None = None
    lease_expires_at: str | None = None
    deadline_at: str | None = None
    next_attempt_at: str | None = None


@dataclass(frozen=True)
class EventAcknowledgmentWorkerResult:
    """False is not proof of rollback. Receipt bodies are protected application data."""

    receipt_confirmed: bool = False
    receipt: EventConsumerTransactionResult | None = field(default=None, repr=False)
    acknowledgment: EventAcknowledgmentWorkerStatus | None = None


_MESSAGES = {
    "event_consumer_preparation_timeout": "event consumer preparation budget expired",
    "event_acknowledgment_lease_lost": "event acknowledgment lease expired, replaced or unavailable",
    "event_acknowledgment_unconfirmed": "event acknowledgment callback outcome unconfirmed",
    "event_acknowledgment_worker_busy": "event acknowledgment worker operation already running",
    "event_consumer_receipt_missing": "no committed event consumer receipt",
    "event_consumer_receipt_committed": "event consumer receipt already committed; complete instead of rejecting",
}


class EventAcknowledgmentWorkerError(RuntimeError):
    """Fixed message/code and bounded status; no remote body, HTTP request or transport cause."""

    def __init__(
        self,
        code: str,
        result: EventAcknowledgmentWorkerResult = EventAcknowledgmentWorkerResult(),
        status: int | None = None,
    ):
        if code not in _MESSAGES:
            raise ValueError("invalid acknowledgment worker error code")
        super().__init__(_MESSAGES[code])
        self.code, self.result, self.status = code, result, status


class EventAcknowledgmentWorkerCancelled(asyncio.CancelledError):
    """Native cancellation category, with confirmed receipt progress preserved."""

    def __init__(self, result: EventAcknowledgmentWorkerResult = EventAcknowledgmentWorkerResult()):
        super().__init__("event acknowledgment worker interrupted")
        self.result = result


class EventAcknowledgmentWorkerCancellation:
    """Read-only cancellation view passed to business callbacks. Never clears owner/lease loss."""

    __slots__ = ("_control",)

    def __init__(self, control: Control):
        self._control = control

    def is_set(self) -> bool:
        return self._control.stopped()

    async def wait(self) -> None:
        await self._control.event.wait()

    def check(self) -> None:
        self._control.check()


@dataclass(frozen=True)
class Budget:
    attempts: int
    timeout_ms: int
    backoff_ms: int
    interval_ms: float


def settings(options: EventAcknowledgmentWorkerOptions | None, seconds: int) -> Budget:
    if options is None:
        options = EventAcknowledgmentWorkerOptions()
    if not isinstance(options, EventAcknowledgmentWorkerOptions):
        raise ValueError("invalid acknowledgment worker options")
    maximum = min(5000, seconds * 1000 // 6)
    timeout = maximum if options.callback_timeout_ms is None else options.callback_timeout_ms
    backoff = min(100, seconds * 1000 // 12) if options.retry_backoff_ms is None else options.retry_backoff_ms
    if (
        type(options.callback_attempts) is not int
        or not 1 <= options.callback_attempts <= 10
        or type(timeout) is not int
        or not 1 <= timeout <= maximum
        or type(backoff) is not int
        or not 1 <= backoff <= seconds * 1000 / 3
    ):
        raise ValueError("invalid acknowledgment worker callback budget")
    return Budget(options.callback_attempts, timeout, backoff, seconds * 1000 / 3)


def reason_code(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[a-z0-9_.-]{1,128}", value) is not None


class CallbackFailure(Exception):
    def __init__(self, status: int | None = None, delay_ms: float | None = None, lost: bool = False):
        super().__init__("event acknowledgment callback unconfirmed")
        self.status, self.delay_ms, self.lost = status, delay_ms, lost


def callback_error(error: CallbackFailure, result: EventAcknowledgmentWorkerResult) -> EventAcknowledgmentWorkerError:
    lost = error.lost or error.status in (404, 409, 410)
    return EventAcknowledgmentWorkerError(
        "event_acknowledgment_lease_lost" if lost else "event_acknowledgment_unconfirmed",
        result,
        error.status,
    )


class DeliveryFence(Protocol):
    @property
    def invocation_id(self) -> str: ...

    @property
    def attempt(self) -> int: ...

    @property
    def replay_generation(self) -> int: ...


def observation(delivery: DeliveryFence, raw: Any) -> EventAcknowledgmentWorkerStatus:
    if not isinstance(raw, dict):
        raise CallbackFailure()
    if (
        not isinstance(raw.get("invocation_id"), str)
        or raw["invocation_id"].lower() != delivery.invocation_id
        or type(raw.get("attempt")) is not int
        or raw["attempt"] != delivery.attempt
        or type(raw.get("replay_generation")) is not int
        or raw["replay_generation"] != delivery.replay_generation
    ):
        raise CallbackFailure(lost=True)
    if (
        not isinstance(raw.get("state"), str)
        or re.fullmatch(r"[a-z_]{1,32}", raw["state"]) is None
        or raw.get("processing_state")
        not in (
            "waiting",
            "delivering",
            "processing",
            "expired",
            "completed",
            "failed",
            "dead_letter",
            "cancelled",
            "superseded",
        )
        or "disposition" in raw
        and raw["disposition"] not in ("retry", "dead-letter")
        or "failure_reason" in raw
        and raw["failure_reason"] != ""
        and not reason_code(raw["failure_reason"])
    ):
        raise CallbackFailure()
    minimum, maximum = timestamp("0001-01-01T00:00:00Z"), timestamp("9999-12-31T23:59:59.999999999Z")
    for key in ("lease_expires_at", "deadline_at", "next_attempt_at"):
        if key in raw:
            at = timestamp(raw[key])
            if at is None or not minimum < at <= maximum:
                raise CallbackFailure()
    # Copy only the public bounded fields. Never retain additional server properties.
    return EventAcknowledgmentWorkerStatus(
        delivery.invocation_id,
        delivery.attempt,
        delivery.replay_generation,
        raw["state"],
        raw["processing_state"],
        raw.get("disposition"),
        raw.get("failure_reason"),
        raw.get("lease_expires_at"),
        raw.get("deadline_at"),
        raw.get("next_attempt_at"),
    )


def renewed(status: EventAcknowledgmentWorkerStatus) -> None:
    if (
        status.state != "dispatching"
        or status.processing_state != "processing"
        or status.disposition
        or status.failure_reason
    ):
        raise CallbackFailure(lost=True)
    expiry, deadline = timestamp(status.lease_expires_at), timestamp(status.deadline_at)
    if expiry is None or deadline is None or expiry > deadline:
        raise CallbackFailure()


class Control:
    def __init__(self, source: asyncio.Event | None = None, owner: asyncio.Task[Any] | None = None):
        self.event = asyncio.Event()
        self.source, self.owner = source, owner
        self.failure: EventAcknowledgmentWorkerError | None = None
        self.result = EventAcknowledgmentWorkerResult()

    def stop(self, failure: EventAcknowledgmentWorkerError | None = None) -> None:
        if not self.event.is_set():
            self.failure = failure
            self.event.set()

    def stopped(self) -> bool:
        # Observe the borrowed owner controls synchronously at SQL/commit checkpoints,
        # even before the forwarding task or caller's cancellation handler runs.
        if self.source is not None and self.source.is_set() or self.owner is not None and self.owner.cancelling():
            self.stop()
        return self.event.is_set()

    def check(self) -> None:
        if self.stopped():
            if self.failure is not None:
                raise EventAcknowledgmentWorkerError(self.failure.code, self.result, self.failure.status) from None
            raise EventAcknowledgmentWorkerCancelled(self.result) from None


async def pause(milliseconds: float, control: Control) -> None:
    control.check()
    try:
        await asyncio.wait_for(control.event.wait(), milliseconds / 1000)
    except TimeoutError:
        pass
    control.check()


_T = TypeVar("_T")


async def owned_operation(operation: Callable[[Control], Awaitable[_T]], stop_event: asyncio.Event | None) -> _T:
    """Signal cancellation, shield active SQL/cleanup, and join the owned operation."""
    if stop_event is not None and not isinstance(stop_event, asyncio.Event):
        raise ValueError("invalid event acknowledgment stop event")
    current = asyncio.current_task()
    if stop_event is not None and stop_event.is_set() or current is not None and current.cancelling():
        raise EventAcknowledgmentWorkerCancelled()
    control = Control(stop_event, current)

    async def forward() -> None:
        try:
            await stop_event.wait()
        except Exception:
            control.stop(EventAcknowledgmentWorkerError("event_acknowledgment_unconfirmed"))
        else:
            control.stop()

    async def owned() -> _T:
        watcher = asyncio.create_task(forward()) if stop_event is not None else None
        try:
            control.check()
            return await operation(control)
        finally:
            if watcher is not None:
                watcher.cancel()
                await asyncio.gather(watcher, return_exceptions=True)

    pending = asyncio.create_task(owned())
    while True:
        try:
            return await asyncio.shield(pending)
        except asyncio.CancelledError:
            if pending.done():
                try:
                    return pending.result()
                except asyncio.CancelledError:
                    raise EventAcknowledgmentWorkerCancelled(control.result) from None
            control.stop()
