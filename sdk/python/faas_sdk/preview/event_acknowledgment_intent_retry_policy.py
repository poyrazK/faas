"""Optional durable retry scheduling for saved acknowledgment callbacks."""

from __future__ import annotations

import hashlib
from dataclasses import dataclass


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentRetryPolicy:
    """Whole-millisecond exponential full-jitter caps, not minimum waits.

    Values must be native integers, 1..3600000, with maximum >= initial.
    This policy schedules future recovery without a lifetime attempt limit.
    """

    initial_backoff_ms: int = 1000
    max_backoff_ms: int = 300000


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentStoredRetryPolicy:
    """Payload-free versioned binding read from an intent row."""

    version: int
    initial_backoff_ms: int
    max_backoff_ms: int
    max_delivery_attempts: int | None = None


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentRetryBudget:
    """Opt-in total committed claims, native integer 1..2147483647.

    Timing is optional; the current grant is separate from this initial binding.
    """

    max_delivery_attempts: int
    retry_policy: EventAcknowledgmentIntentRetryPolicy | None = None


def _staging_policy(
    retry_policy: EventAcknowledgmentIntentRetryPolicy | None,
    retry_budget: EventAcknowledgmentIntentRetryBudget | None,
) -> EventAcknowledgmentIntentStoredRetryPolicy | None:
    """Copy and validate before asynchronous work. Never alter fallback semantics."""
    if retry_budget is None:
        policy = _retry_schedule(None, retry_policy).retry_policy
        return (
            None
            if policy is None
            else EventAcknowledgmentIntentStoredRetryPolicy(1, policy.initial_backoff_ms, policy.max_backoff_ms)
        )
    if type(retry_budget) is not EventAcknowledgmentIntentRetryBudget or retry_policy is not None:
        raise ValueError("invalid acknowledgment intent retry budget")
    maximum = retry_budget.max_delivery_attempts
    if type(maximum) is not int or not 1 <= maximum <= 2147483647:
        raise ValueError("invalid acknowledgment intent retry budget")
    timing = retry_budget.retry_policy
    policy = _retry_schedule(None, EventAcknowledgmentIntentRetryPolicy() if timing is None else timing).retry_policy
    if policy is None:
        raise ValueError("invalid acknowledgment intent retry budget")
    return EventAcknowledgmentIntentStoredRetryPolicy(2, policy.initial_backoff_ms, policy.max_backoff_ms, maximum)


@dataclass(frozen=True, slots=True)
class _RetrySchedule:
    backoff_ms: int | None
    retry_policy: EventAcknowledgmentIntentRetryPolicy | None = None


def _retry_schedule(
    backoff_ms: int | None, retry_policy: EventAcknowledgmentIntentRetryPolicy | None
) -> _RetrySchedule:
    if retry_policy is None:
        delay = 1000 if backoff_ms is None else backoff_ms
        if type(delay) is not int or not 1 <= delay <= 3600000:
            raise ValueError("invalid acknowledgment intent backoff")
        return _RetrySchedule(delay)
    if backoff_ms is not None or type(retry_policy) is not EventAcknowledgmentIntentRetryPolicy:
        raise ValueError("invalid acknowledgment intent retry policy")
    initial, maximum = retry_policy.initial_backoff_ms, retry_policy.max_backoff_ms
    if type(initial) is not int or type(maximum) is not int or not 1 <= initial <= maximum <= 3600000:
        raise ValueError("invalid acknowledgment intent retry policy")
    return _RetrySchedule(None, EventAcknowledgmentIntentRetryPolicy(initial, maximum))


def _retry_delay(schedule: _RetrySchedule, claim: bytes, attempts: int) -> int:
    """Inputs are normalized; attempts come from the committed claim update."""
    policy = schedule.retry_policy
    if policy is None:
        return 1000 if schedule.backoff_ms is None else schedule.backoff_ms
    cap, n = policy.initial_backoff_ms, 1
    while n < attempts and cap < policy.max_backoff_ms:
        cap = min(policy.max_backoff_ms, cap * 2)
        n += 1
    # Reuse entropy generated before claim I/O. No random-source read follows
    # uncertain HTTP and no raw claim-token bits enter the persisted delay.
    entropy = hashlib.sha256(b"gregale-event-acknowledgment-intent-jitter-v1\n" + claim).digest()
    return int.from_bytes(entropy[:8], "big") % cap + 1
