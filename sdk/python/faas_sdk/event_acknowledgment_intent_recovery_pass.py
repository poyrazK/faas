"""Explicit finite callback recovery passes. Hosts own scheduling and connection scopes."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import Literal

from ._event_acknowledgment_intent_recovery_pass_runtime import ConnectionFactory, Policy, run_pass
from ._event_acknowledgment_protocol import (
    Control,
    EventAcknowledgmentWorkerCancelled,
    EventAcknowledgmentWorkerOptions,
    owned_operation,
    settings,
)
from .client import AuthenticatedClient
from .event_acknowledgment_intent_recovery import _duration
from .event_acknowledgment_intent_retry_policy import EventAcknowledgmentIntentRetryPolicy, _retry_schedule
from .event_consumer_transactions import EventConsumerCommitUnknownError, _uuid


@dataclass(frozen=True)
class EventAcknowledgmentIntentRecoveryPassOptions:
    max_attempts: int = 100
    concurrency: int = 4
    claim_duration_ms: int = 30000
    backoff_ms: int | None = None
    callback_options: EventAcknowledgmentWorkerOptions | None = None
    retry_policy: EventAcknowledgmentIntentRetryPolicy | None = None


@dataclass(frozen=True)
class EventAcknowledgmentIntentRecoveryPassResult:
    """Aggregate observations, without per-job data. Cleanup/uncertainty can overlap known states."""

    stop_reason: Literal["limit", "empty", "interrupted", "failed", "commit_unknown"]
    attempts: int = 0
    confirmed: int = 0
    unconfirmed: int = 0
    superseded: int = 0
    empty: int = 0
    claim_lost: int = 0
    interrupted: int = 0
    failed: int = 0
    commit_unknown: int = 0
    cleanup_failures: int = 0
    parked: int = 0


class EventAcknowledgmentIntentRecoveryPassError(RuntimeError):
    """Fixed failure with joined, frozen partial progress and no raw cause."""

    code = "event_acknowledgment_intent_recovery_pass_failed"

    def __init__(self, result: EventAcknowledgmentIntentRecoveryPassResult):
        super().__init__("acknowledgment intent recovery pass failed; inspect retained intents")
        self.result = result


class EventAcknowledgmentIntentRecoveryPassCommitUnknownError(EventConsumerCommitUnknownError):
    """Preserve the uncertain-COMMIT category even when cancellation is also observed."""

    code = "event_acknowledgment_intent_recovery_pass_commit_unknown"

    def __init__(self, result: EventAcknowledgmentIntentRecoveryPassResult):
        super().__init__("acknowledgment intent recovery commit outcome unknown; reconcile on fresh connections")
        self.result = result


class EventAcknowledgmentIntentRecoveryPassCancelled(asyncio.CancelledError):
    """Native cancellation category with frozen progress after all owned work joins."""

    def __init__(self, result: EventAcknowledgmentIntentRecoveryPassResult):
        super().__init__("acknowledgment intent recovery pass interrupted")
        self.result = result


def _policy(options: EventAcknowledgmentIntentRecoveryPassOptions) -> Policy:
    if not isinstance(options, EventAcknowledgmentIntentRecoveryPassOptions):
        raise ValueError("invalid acknowledgment intent recovery pass options")
    limit, concurrency = options.max_attempts, options.concurrency
    duration, backoff, callback = options.claim_duration_ms, options.backoff_ms, options.callback_options
    if type(limit) is not int or not 1 <= limit <= 10000 or type(concurrency) is not int or not 1 <= concurrency <= 32:
        raise ValueError("invalid acknowledgment intent recovery pass options")
    _duration(duration)
    schedule = _retry_schedule(backoff, options.retry_policy)
    if callback is not None:
        if not isinstance(callback, EventAcknowledgmentWorkerOptions):
            raise ValueError("invalid acknowledgment intent recovery callback options")
        callback = EventAcknowledgmentWorkerOptions(
            callback.callback_attempts, callback.callback_timeout_ms, callback.retry_backoff_ms
        )
    settings(callback, 300)  # The existing primitive additionally checks each row's actual lease.
    return Policy(limit, concurrency, duration, schedule.backoff_ms, callback, schedule.retry_policy)


async def arecover_event_acknowledgment_intents(
    client: AuthenticatedClient,
    connection_factory: ConnectionFactory,
    account_id: str,
    app_id: str,
    options: EventAcknowledgmentIntentRecoveryPassOptions,
    *,
    stop_event: asyncio.Event | None = None,
) -> EventAcknowledgmentIntentRecoveryPassResult:
    """Run one finite concurrent pass, using a fresh exclusive connection scope per admission.

    Empty probes consume the budget and stop admission without proving a globally
    empty backlog. All admitted work and scope cleanup joins before return/raise.
    """
    if not isinstance(client, AuthenticatedClient) or not callable(connection_factory):
        raise ValueError("authenticated SDK client and recovery connection factory required")
    account, app = _uuid(account_id), _uuid(app_id)
    policy = _policy(options)

    async def recover(control: Control) -> EventAcknowledgmentIntentRecoveryPassResult:
        return EventAcknowledgmentIntentRecoveryPassResult(
            **await run_pass(client, connection_factory, account, app, policy, control)
        )

    result = EventAcknowledgmentIntentRecoveryPassResult("interrupted")
    try:
        result = await owned_operation(recover, stop_event)
    except EventAcknowledgmentWorkerCancelled:
        pass  # Pre-admission cancellation still exposes a zero-attempt pass result.
    if result.stop_reason == "commit_unknown":
        raise EventAcknowledgmentIntentRecoveryPassCommitUnknownError(result)
    if result.stop_reason == "interrupted":
        raise EventAcknowledgmentIntentRecoveryPassCancelled(result)
    if result.stop_reason == "failed":
        raise EventAcknowledgmentIntentRecoveryPassError(result)
    return result
