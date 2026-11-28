"""Bounded, best-effort preparation telemetry. No identities, capabilities or results."""

from __future__ import annotations

import asyncio
import inspect
import time
from collections.abc import Awaitable, Callable
from contextvars import Context
from dataclasses import dataclass, field
from typing import Any, Literal, TypeVar

from ._event_acknowledgment_protocol import EventAcknowledgmentWorkerError
from ._event_consumer_processing_store import EventConsumerProcessingError
from .event_acknowledgment_intents import EventAcknowledgmentIntentError
from .event_consumer_transactions import EventConsumerCommitUnknownError, EventConsumerConflictError

EventAcknowledgmentPreparationStage = Literal[
    "receipt_reconciliation", "platform_start", "claim", "local_renewal", "preparation", "local_handoff", "completion"
]
EventAcknowledgmentPreparationOutcome = Literal["committed", "commit_unknown", "cancelled", "failed"]
EventAcknowledgmentPreparationReason = Literal[
    "cancelled",
    "preparation_timeout",
    "claim_busy",
    "local_lease_lost",
    "local_operation_busy",
    "local_store_failed",
    "platform_lease_lost",
    "platform_unconfirmed",
    "receipt_conflict",
    "receipt_missing",
    "intent_conflict",
    "intent_superseded",
    "intent_store_failed",
    "commit_unknown",
    "failed",
]


@dataclass(frozen=True, slots=True)
class _Event:
    elapsed_ms: int


@dataclass(frozen=True, slots=True)
class _Started(_Event):
    kind: Literal["started"] = field(default="started", init=False)


@dataclass(frozen=True, slots=True)
class _ClaimAcquired(_Event):
    kind: Literal["claim_acquired"] = field(default="claim_acquired", init=False)


@dataclass(frozen=True, slots=True)
class _PreparationStarted(_Event):
    kind: Literal["preparation_started"] = field(default="preparation_started", init=False)


@dataclass(frozen=True, slots=True)
class _PreparationFinished(_Event):
    outcome: Literal["returned", "threw"]
    duration_ms: int
    kind: Literal["preparation_finished"] = field(default="preparation_finished", init=False)


@dataclass(frozen=True, slots=True)
class _RenewalFailed(_Event):
    lease: Literal["local", "platform"]
    reason: EventAcknowledgmentPreparationReason
    kind: Literal["renewal_failed"] = field(default="renewal_failed", init=False)


@dataclass(frozen=True, slots=True)
class _IntentCommitted(_Event):
    replayed: bool
    kind: Literal["intent_committed"] = field(default="intent_committed", init=False)


@dataclass(frozen=True, slots=True)
class _CommitUnknown(_Event):
    stage: EventAcknowledgmentPreparationStage
    kind: Literal["commit_unknown"] = field(default="commit_unknown", init=False)


@dataclass(frozen=True, slots=True)
class _Shutdown(_Event):
    stage: EventAcknowledgmentPreparationStage
    outcome: EventAcknowledgmentPreparationOutcome
    reason: EventAcknowledgmentPreparationReason | None = None
    kind: Literal["shutdown"] = field(default="shutdown", init=False)


EventAcknowledgmentPreparationEvent = (
    _Started
    | _ClaimAcquired
    | _PreparationStarted
    | _PreparationFinished
    | _RenewalFailed
    | _IntentCommitted
    | _CommitUnknown
    | _Shutdown
)
_T = TypeVar("_T")


def _reason(error: BaseException) -> EventAcknowledgmentPreparationReason:
    try:
        if isinstance(error, EventConsumerCommitUnknownError):
            return "commit_unknown"
        if isinstance(error, asyncio.CancelledError):
            return "cancelled"
        if isinstance(error, EventConsumerConflictError):
            return "receipt_conflict"
        if isinstance(error, EventConsumerProcessingError):
            return {
                "event_consumer_processing_busy": "claim_busy",
                "event_consumer_processing_lease_lost": "local_lease_lost",
                "event_consumer_processing_operation_busy": "local_operation_busy",
                "event_consumer_processing_store": "local_store_failed",
            }.get(error.code, "failed")
        if isinstance(error, EventAcknowledgmentWorkerError):
            return {
                "event_consumer_preparation_timeout": "preparation_timeout",
                "event_acknowledgment_lease_lost": "platform_lease_lost",
                "event_acknowledgment_unconfirmed": "platform_unconfirmed",
                "event_consumer_receipt_missing": "receipt_missing",
            }.get(error.code, "failed")
        if isinstance(error, EventAcknowledgmentIntentError):
            return {
                "event_acknowledgment_intent_conflict": "intent_conflict",
                "event_acknowledgment_intent_superseded": "intent_superseded",
                "event_acknowledgment_intent_store": "intent_store_failed",
            }.get(error.code, "failed")
    except BaseException:
        pass  # Hostile application error attributes cannot influence telemetry.
    return "failed"


def _clock() -> int:
    try:
        value = time.monotonic_ns()
        return value if type(value) is int else 0
    except BaseException:
        return 0


def _duration(start: int) -> int:
    return max(0, min(9007199254740991, (_clock() - start) // 1000000))


def _consume_future(future: asyncio.Future[Any]) -> None:
    try:
        future.exception()
    except BaseException:
        pass


def _discard_return(value: Any) -> None:
    # Never schedule or drive observer work. Do not cancel/join borrowed futures.
    if inspect.iscoroutine(value):
        if inspect.getcoroutinestate(value) == inspect.CORO_CREATED:
            value.close()
    elif isinstance(value, asyncio.Future):
        if value.done():
            _consume_future(value)
        else:
            value.add_done_callback(_consume_future, context=Context())


class PreparationEvents:
    """Internal fixed metadata only; never retains errors, inputs or prepared values."""

    def __init__(self, observer: Callable[[EventAcknowledgmentPreparationEvent], None] | None):
        self._observer, self._started = observer, _clock()
        self._stage: EventAcknowledgmentPreparationStage = "receipt_reconciliation"
        self._outcome: EventAcknowledgmentPreparationOutcome = "failed"
        self._reason: EventAcknowledgmentPreparationReason | None = None
        self._unknown = self._committed = self._closed = False
        self._renewals: set[str] = set()
        self._emit(_Started)

    def stage(self, stage: EventAcknowledgmentPreparationStage) -> None:
        self._stage = stage

    def claim_acquired(self) -> None:
        self._emit(_ClaimAcquired)

    async def prepare(self, operation: Callable[[], Awaitable[_T]]) -> _T:
        if self._observer is None:
            return await operation()
        self._emit(_PreparationStarted)
        started = _clock()
        failure: BaseException | None = None
        try:
            prepared = await operation()
        except BaseException as error:
            failure = error
        if failure is not None:
            self._emit(_PreparationFinished, outcome="threw", duration_ms=_duration(started))
            raise failure
        self._emit(_PreparationFinished, outcome="returned", duration_ms=_duration(started))
        return prepared

    def renewal_failed(
        self, lease: Literal["local", "platform"], error: BaseException, already_stopped: bool = False
    ) -> None:
        if self._observer is None:
            return
        reason = _reason(error)
        if lease in self._renewals or already_stopped and reason != "commit_unknown":
            return
        self._renewals.add(lease)
        self._emit(_RenewalFailed, lease=lease, reason=reason)
        if reason == "commit_unknown":
            self._commit_unknown("local_renewal" if lease == "local" else self._stage)

    def intent_committed(self, replayed: bool) -> None:
        if not self._committed:
            self._committed = True
            self._emit(_IntentCommitted, replayed=replayed)

    def failed(self, error: BaseException) -> None:
        if self._observer is None:
            return
        self._reason = _reason(error)
        self._outcome = (
            "commit_unknown"
            if self._reason == "commit_unknown"
            else "cancelled"
            if self._reason == "cancelled"
            else "failed"
        )
        if self._outcome == "commit_unknown":
            self._commit_unknown(self._stage)

    def _commit_unknown(self, stage: EventAcknowledgmentPreparationStage) -> None:
        if not self._unknown:
            self._unknown = True
            self._emit(_CommitUnknown, stage=stage)

    def shutdown(self) -> None:
        self._emit(
            _Shutdown,
            stage=self._stage,
            outcome="committed" if self._committed else self._outcome,
            reason=None if self._committed else self._reason,
        )
        self._closed = True

    def _emit(self, event_type: Any, **detail: Any) -> None:
        if self._observer is None or self._closed:
            return
        try:
            _discard_return(self._observer(event_type(elapsed_ms=_duration(self._started), **detail)))
        except BaseException:
            pass  # Best-effort diagnostics cannot choose processing/commit outcomes.


async def observe_preparation(
    observer: Callable[[EventAcknowledgmentPreparationEvent], None] | None,
    operation: Callable[[PreparationEvents], Awaitable[_T]],
) -> _T:
    events = PreparationEvents(observer)
    if observer is None:
        return await operation(events)
    failure: BaseException | None = None
    try:
        result = await operation(events)
    except BaseException as error:
        failure = error
    # Notify outside active exception handlers, including shutdown: an observer's
    # own exception must not inherit the processing error as implicit context.
    if failure is not None:
        events.failed(failure)
    events.shutdown()
    if failure is not None:
        raise failure
    return result
