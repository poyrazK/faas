"""Finite same-loop recovery admission and joined connection scopes; no new SQL."""

from __future__ import annotations

import asyncio
import inspect
from collections.abc import Callable
from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass
from typing import Any, Literal

from ._event_acknowledgment_protocol import Control, EventAcknowledgmentWorkerOptions
from .client import AuthenticatedClient
from .event_acknowledgment_intent_recovery import arecover_next_event_acknowledgment_intent
from .event_acknowledgment_intent_retry_policy import EventAcknowledgmentIntentRetryPolicy
from .event_acknowledgment_intents import EventAcknowledgmentIntentError, EventAcknowledgmentIntentStatus
from .event_consumer_transactions import EventConsumerCommitUnknownError

ConnectionFactory = Callable[[], AbstractAsyncContextManager[Any]]
Kind = Literal[
    "confirmed", "unconfirmed", "parked", "superseded", "empty", "claim_lost", "interrupted", "failed", "commit_unknown"
]
_KINDS = (
    "confirmed",
    "unconfirmed",
    "parked",
    "superseded",
    "empty",
    "claim_lost",
    "interrupted",
    "failed",
    "commit_unknown",
)
_STOP = ("empty", "interrupted", "failed", "commit_unknown")


@dataclass(frozen=True)
class Policy:
    max_attempts: int
    concurrency: int
    claim_duration_ms: int
    backoff_ms: int | None
    callback_options: EventAcknowledgmentWorkerOptions | None
    retry_policy: EventAcknowledgmentIntentRetryPolicy | None


@dataclass(frozen=True)
class _Attempt:
    kind: Kind
    cleanup_failed: bool
    interrupted: bool
    commit_unknown: bool


def _kind(error: BaseException, *, operation: bool) -> Kind:
    try:
        if isinstance(error, EventConsumerCommitUnknownError):
            return "commit_unknown"
        if isinstance(error, asyncio.CancelledError):
            return "interrupted"
        if operation and isinstance(error, EventAcknowledgmentIntentError):
            code = error.code
            if type(code) is str:
                if code == "event_acknowledgment_intent_empty":
                    return "empty"
                if code == "event_acknowledgment_intent_claim_lost":
                    return "claim_lost"
    except BaseException:
        pass
    return "failed"


class _Progress:
    def __init__(self) -> None:
        self.attempts = 0
        self.counts = dict.fromkeys(_KINDS, 0)
        self.cleanup_failures = 0
        self.closed = self.interrupted = self.unexpected = False

    def stop(self, kind: Kind) -> None:
        if kind in _STOP:
            self.closed = True

    def record(self, attempt: _Attempt) -> None:
        if attempt.kind != "commit_unknown":
            self.counts[attempt.kind] += 1
        self.counts["commit_unknown"] += int(attempt.commit_unknown)
        self.cleanup_failures += int(attempt.cleanup_failed)
        self.interrupted |= attempt.interrupted

    def snapshot(self, control: Control) -> dict[str, Any]:
        if self.counts["commit_unknown"]:
            reason = "commit_unknown"
        elif self.interrupted or control.stopped():
            reason = "interrupted"
        elif self.counts["failed"] or self.cleanup_failures or self.unexpected:
            reason = "failed"
        elif self.counts["empty"]:
            reason = "empty"
        else:
            reason = "limit"
        return {
            "stop_reason": reason,
            "attempts": self.attempts,
            **self.counts,
            "cleanup_failures": self.cleanup_failures,
        }


async def _attempt(
    client: AuthenticatedClient,
    factory: ConnectionFactory,
    account: str,
    app: str,
    policy: Policy,
    control: Control,
    progress: _Progress,
) -> _Attempt:
    kind: Kind | None = None
    operation_failure: BaseException | None = None
    cleanup_failed = interrupted = unknown = False
    connection = None
    try:
        control.check()
        manager = factory()
        if inspect.iscoroutine(manager):
            if inspect.getcoroutinestate(manager) == inspect.CORO_CREATED:
                manager.close()
            raise ValueError("connection factory must return an async context manager")
        async with manager as connection:
            try:
                control.check()
                status = await arecover_next_event_acknowledgment_intent(
                    client,
                    connection,
                    account,
                    app,
                    claim_duration_ms=policy.claim_duration_ms,
                    backoff_ms=policy.backoff_ms,
                    retry_policy=policy.retry_policy,
                    callback_options=policy.callback_options,
                    stop_event=control.event,
                )
                if type(status) is not EventAcknowledgmentIntentStatus or status.state not in (
                    "confirmed",
                    "unconfirmed",
                    "parked",
                    "superseded",
                ):
                    raise ValueError("invalid acknowledgment intent recovery outcome")
                kind = status.state
                del status
            except BaseException as error:
                operation_failure = error
                kind = _kind(error, operation=True)
                progress.stop(kind)  # Stop admissions before potentially slow scope cleanup.
                raise
    except BaseException as error:
        outer = _kind(error, operation=False)
        interrupted = outer == "interrupted"
        unknown = outer == "commit_unknown"
        if operation_failure is None and kind is None:
            kind = outer  # Acquisition failure; an arbitrary factory cannot report an empty probe.
        elif error is not operation_failure:
            cleanup_failed = True
            progress.closed = True
            if connection is not None:
                try:
                    await connection.close()
                except BaseException as closing_error:
                    closing = _kind(closing_error, operation=False)
                    unknown |= closing == "commit_unknown"
                    interrupted |= closing == "interrupted"
    # Context suppression/replacement cannot erase the operation's outcome or uncertainty.
    kind = "failed" if kind is None else kind
    interrupted |= kind == "interrupted"
    unknown |= kind == "commit_unknown"
    progress.stop(kind)
    return _Attempt(kind, cleanup_failed, interrupted, unknown)


async def run_pass(
    client: AuthenticatedClient,
    factory: ConnectionFactory,
    account: str,
    app: str,
    policy: Policy,
    control: Control,
) -> dict[str, Any]:
    progress = _Progress()

    async def worker() -> None:
        try:
            while not progress.closed and progress.attempts < policy.max_attempts:
                if control.stopped():
                    progress.closed = True
                    return
                # No await separates the check and reservation on this event-loop thread.
                progress.attempts += 1
                progress.record(await _attempt(client, factory, account, app, policy, control, progress))
        except BaseException:
            progress.closed = True
            raise  # gather below joins all workers and consumes unexpected failures.

    tasks = []
    for _ in range(min(policy.concurrency, policy.max_attempts)):
        pending = worker()
        try:
            tasks.append(asyncio.create_task(pending))
        except BaseException as error:
            pending.close()
            progress.closed = progress.unexpected = True
            progress.interrupted |= _kind(error, operation=False) == "interrupted"
            break
    completed = await asyncio.gather(*tasks, return_exceptions=True)
    # No worker/SQL/scope is cancelled or abandoned, including an unexpected worker failure.
    for error in completed:
        if isinstance(error, BaseException):
            progress.unexpected = True
            kind = _kind(error, operation=False)
            progress.interrupted |= kind == "interrupted"
            if kind == "commit_unknown":
                progress.counts["commit_unknown"] += 1
    return progress.snapshot(control)
