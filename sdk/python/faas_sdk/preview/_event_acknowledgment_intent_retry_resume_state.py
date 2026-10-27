"""Private budget-resume framing and historical metadata shared with Go and Node."""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any

from ._event_acknowledgment_intent_policy_adoption_state import (
    _adoption_status,
    _at,
    _canonical_time,
    _invalid_constant,
)
from .event_acknowledgment_intents import (
    EventAcknowledgmentIntentError,
    EventAcknowledgmentIntentStatus,
    _bytes,
    _hash,
    _instant,
    _integer,
    _store_error,
)
from .event_consumer_transactions import _uuid


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentResumeRetryBudgetOptions:
    """Persist the original command UUID, exact fences and new total before issuing."""

    command_id: str
    expected_updated_at: str
    expected_delivery_attempts: int
    expected_delivery_attempt_limit: int
    new_delivery_attempt_limit: int


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentRetryResumeReceipt:
    """Historical local outcome, without protected callback data or a recovery claim."""

    command_id: str
    invocation_id: str
    replay_generation: int
    attempt: int
    outcome: str
    recorded_at: str
    intent: EventAcknowledgmentIntentStatus | None = None


class EventAcknowledgmentIntentRetryResumeError(EventAcknowledgmentIntentError):
    """Only a known committed/replayed rejection exposes its retained receipt."""

    def __init__(self, code: str, receipt: EventAcknowledgmentIntentRetryResumeReceipt | None = None):
        if code not in ("event_acknowledgment_intent_conflict", "event_acknowledgment_intent_not_found"):
            raise ValueError("invalid acknowledgment retry resume rejection")
        super().__init__(code)
        self.receipt = receipt


@dataclass(frozen=True, slots=True)
class _ResumeRequest:
    command_id: str
    invocation_id: str
    generation: int
    attempt: int
    expected_updated_at: str
    expected_delivery_attempts: int
    expected_delivery_attempt_limit: int
    new_delivery_attempt_limit: int
    digest: bytes


def _selection(invocation: str, generation: int, attempt: int) -> tuple[str, int, int]:
    if (
        type(generation) is not int
        or not 0 <= generation <= 9223372036854775807
        or type(attempt) is not int
        or not 1 <= attempt <= 2147483647
    ):
        raise ValueError("invalid acknowledgment retry resume selection")
    return _uuid(invocation), generation, attempt


def _request(
    invocation: str, generation: int, attempt: int, options: EventAcknowledgmentIntentResumeRetryBudgetOptions
) -> _ResumeRequest:
    invocation, generation, attempt = _selection(invocation, generation, attempt)
    if type(options) is not EventAcknowledgmentIntentResumeRetryBudgetOptions:
        raise ValueError("invalid acknowledgment retry resume options")
    attempts, previous, maximum = (
        options.expected_delivery_attempts,
        options.expected_delivery_attempt_limit,
        options.new_delivery_attempt_limit,
    )
    if (
        type(attempts) is not int
        or not 0 <= attempts <= 2147483647
        or type(previous) is not int
        or not 1 <= previous <= 2147483647
        or type(maximum) is not int
        or not max(previous, attempts) < maximum <= 2147483647
    ):
        raise ValueError("invalid acknowledgment retry resume options")
    command, expected = _uuid(options.command_id), _canonical_time(options.expected_updated_at)
    # Fixed compact order matches Go/Node. Native int preserves the full int64
    # generation; timestamp normalization never passes through float/epoch seconds.
    body = json.dumps(
        {
            "version": 1,
            "action": "resume_retry_budget",
            "invocation_id": invocation,
            "replay_generation": generation,
            "attempt": attempt,
            "expected_updated_at": expected,
            "expected_delivery_attempts": attempts,
            "expected_delivery_attempt_limit": previous,
            "new_delivery_attempt_limit": maximum,
        },
        separators=(",", ":"),
        allow_nan=False,
    )
    return _ResumeRequest(
        command,
        invocation,
        generation,
        attempt,
        expected,
        attempts,
        previous,
        maximum,
        _hash("gregale-event-ack-intent-retry-resume-v1\n" + body),
    )


def _validate_status(status: EventAcknowledgmentIntentStatus, request: _ResumeRequest) -> None:
    policy, limit = status.retry_policy, status.delivery_attempt_limit
    if (
        (status.invocation_id, status.replay_generation, status.attempt)
        != (request.invocation_id, request.generation, request.attempt)
        or status.state != "unconfirmed"
        or status.last_code != "unconfirmed"
        or status.claim_expires_at is not None
        or status.delivery_attempts < 1
        or status.delivery_attempts != request.expected_delivery_attempts
        or limit != request.new_delivery_attempt_limit
        or status.delivery_attempts >= limit
        or policy is None
        or policy.version != 2
        or policy.max_delivery_attempts is None
        or policy.max_delivery_attempts > request.expected_delivery_attempt_limit
        or _at(status.updated_at) <= _at(request.expected_updated_at)
    ):
        raise _store_error()


def _snapshot(status: EventAcknowledgmentIntentStatus) -> str:
    policy = status.retry_policy
    if policy is None or policy.version != 2:
        raise _store_error()
    return json.dumps(
        {
            "invocation_id": status.invocation_id,
            "replay_generation": status.replay_generation,
            "attempt": status.attempt,
            "action": status.action,
            **({"reason": status.reason} if status.reason else {}),
            "state": status.state,
            "delivery_attempts": status.delivery_attempts,
            "delivery_attempt_limit": status.delivery_attempt_limit,
            "next_attempt_at": status.next_attempt_at,
            "last_code": status.last_code,
            "created_at": status.created_at,
            "updated_at": status.updated_at,
            "retry_policy": {
                "version": 2,
                "initial_backoff_ms": policy.initial_backoff_ms,
                "max_backoff_ms": policy.max_backoff_ms,
                "max_delivery_attempts": policy.max_delivery_attempts,
            },
        },
        separators=(",", ":"),
        allow_nan=False,
    )


def _receipt(row: Any, request: _ResumeRequest, *, replay: bool) -> EventAcknowledgmentIntentRetryResumeReceipt:
    if not isinstance(row, (tuple, list)) or len(row) != 8:
        raise _store_error()
    command, invocation, generation, attempt, outcome, recorded, body, digest = row
    if (
        _uuid(command) != command
        or _uuid(invocation) != invocation
        or outcome not in ("resumed", "conflict", "not_found")
    ):
        raise _store_error()
    generation, attempt = _integer(generation, 0, 9223372036854775807), _integer(attempt, 1, 2147483647)
    recorded, digest = _canonical_time(_instant(recorded)), _bytes(digest)
    if (command, invocation, generation, attempt, digest) != (
        request.command_id,
        request.invocation_id,
        request.generation,
        request.attempt,
        request.digest,
    ):
        if replay:
            raise EventAcknowledgmentIntentRetryResumeError("event_acknowledgment_intent_conflict")
        raise _store_error()
    status = None
    if outcome == "resumed":
        if type(body) is not str or not 0 < len(body.encode("utf-8")) <= 8192:
            raise _store_error()
        saved = json.loads(body, parse_constant=_invalid_constant)
        if type(saved) is not dict or type(saved.get("retry_policy")) is not dict:
            raise _store_error()
        policy = saved["retry_policy"]
        if type(policy.get("version")) is not int or policy["version"] != 2:
            raise _store_error()
        status = _adoption_status(
            (
                saved.get("invocation_id"),
                saved.get("replay_generation"),
                saved.get("attempt"),
                saved.get("action"),
                saved.get("reason", ""),
                saved.get("state"),
                saved.get("delivery_attempts"),
                _at(saved.get("next_attempt_at")),
                None if saved.get("claim_expires_at") is None else _at(saved["claim_expires_at"]),
                saved.get("last_code", ""),
                _at(saved.get("created_at")),
                _at(saved.get("updated_at")),
                policy.get("version"),
                policy.get("initial_backoff_ms"),
                policy.get("max_backoff_ms"),
                policy.get("max_delivery_attempts"),
                saved.get("delivery_attempt_limit"),
            )
        )
        _validate_status(status, request)
    elif body is not None:
        raise _store_error()
    return EventAcknowledgmentIntentRetryResumeReceipt(
        command, invocation, generation, attempt, outcome, recorded, status
    )
