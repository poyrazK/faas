"""Private budget-adoption framing and historical metadata shared with Go/Node."""

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
from .event_acknowledgment_intent_retry_policy import (
    EventAcknowledgmentIntentRetryBudget,
    EventAcknowledgmentIntentStoredRetryPolicy,
    _staging_policy,
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
class EventAcknowledgmentIntentAdoptRetryBudgetOptions:
    """Persist the command and original options durably before calling.

    None expected_policy requires unbound work; complete v1 timing must be retained.
    retry_budget is a total above consumed claims, not a grant of additional claims.
    """

    command_id: str
    expected_updated_at: str
    expected_delivery_attempts: int
    retry_budget: EventAcknowledgmentIntentRetryBudget
    expected_policy: EventAcknowledgmentIntentStoredRetryPolicy | None = None


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentBudgetAdoptionReceipt:
    """Historical local evidence; no callback data, current claim or remote result."""

    command_id: str
    invocation_id: str
    replay_generation: int
    attempt: int
    outcome: str
    recorded_at: str
    intent: EventAcknowledgmentIntentStatus | None = None


class EventAcknowledgmentIntentBudgetAdoptionError(EventAcknowledgmentIntentError):
    """Only known committed/replayed rejections expose a historical receipt."""

    def __init__(self, code: str, receipt: EventAcknowledgmentIntentBudgetAdoptionReceipt | None = None):
        if code not in ("event_acknowledgment_intent_conflict", "event_acknowledgment_intent_not_found"):
            raise ValueError("invalid acknowledgment budget adoption rejection")
        super().__init__(code)
        self.receipt = receipt


@dataclass(frozen=True, slots=True)
class _BudgetAdoptionRequest:
    command_id: str
    invocation_id: str
    generation: int
    attempt: int
    expected_updated_at: str
    expected_delivery_attempts: int
    expected_policy_version: int
    expected_initial: int
    expected_maximum: int
    initial: int
    maximum: int
    total: int
    digest: bytes


def _selection(invocation: str, generation: int, attempt: int) -> tuple[str, int, int]:
    if (
        type(generation) is not int
        or not 0 <= generation <= 9223372036854775807
        or type(attempt) is not int
        or not 1 <= attempt <= 2147483647
    ):
        raise ValueError("invalid acknowledgment budget adoption selection")
    return _uuid(invocation), generation, attempt


def _request(
    invocation: str, generation: int, attempt: int, options: EventAcknowledgmentIntentAdoptRetryBudgetOptions
) -> _BudgetAdoptionRequest:
    invocation, generation, attempt = _selection(invocation, generation, attempt)
    if type(options) is not EventAcknowledgmentIntentAdoptRetryBudgetOptions:
        raise ValueError("invalid acknowledgment budget adoption options")
    attempts = options.expected_delivery_attempts
    if type(attempts) is not int or not 0 <= attempts <= 2147483647:
        raise ValueError("invalid acknowledgment budget adoption options")
    binding = _staging_policy(None, options.retry_budget)
    if (
        binding is None
        or binding.version != 2
        or binding.max_delivery_attempts is None
        or binding.max_delivery_attempts <= attempts
    ):
        raise ValueError("invalid acknowledgment budget adoption options")
    expected_version = expected_initial = expected_maximum = 0
    policy = options.expected_policy
    if policy is not None:
        if (
            type(policy) is not EventAcknowledgmentIntentStoredRetryPolicy
            or type(policy.version) is not int
            or policy.version != 1
            or type(policy.initial_backoff_ms) is not int
            or type(policy.max_backoff_ms) is not int
            or not 1 <= policy.initial_backoff_ms <= policy.max_backoff_ms <= 3600000
            or policy.max_delivery_attempts is not None
            and (type(policy.max_delivery_attempts) is not int or policy.max_delivery_attempts != 0)
            or policy.initial_backoff_ms != binding.initial_backoff_ms
            or policy.max_backoff_ms != binding.max_backoff_ms
        ):
            raise ValueError("invalid acknowledgment budget adoption expected policy")
        expected_version, expected_initial, expected_maximum = 1, policy.initial_backoff_ms, policy.max_backoff_ms
    command, expected = _uuid(options.command_id), _canonical_time(options.expected_updated_at)
    # Exact compact order matches Go/Node. Never convert generation or timestamps
    # through floating point; all caller-owned data is copied before asynchronous I/O.
    body = json.dumps(
        {
            "version": 1,
            "action": "adopt_retry_budget",
            "invocation_id": invocation,
            "replay_generation": generation,
            "attempt": attempt,
            "expected_updated_at": expected,
            "expected_delivery_attempts": attempts,
            "expected_policy_version": expected_version,
            "expected_initial_backoff_ms": expected_initial,
            "expected_max_backoff_ms": expected_maximum,
            "policy_version": 2,
            "initial_backoff_ms": binding.initial_backoff_ms,
            "max_backoff_ms": binding.max_backoff_ms,
            "max_delivery_attempts": binding.max_delivery_attempts,
        },
        separators=(",", ":"),
        allow_nan=False,
    )
    return _BudgetAdoptionRequest(
        command,
        invocation,
        generation,
        attempt,
        expected,
        attempts,
        expected_version,
        expected_initial,
        expected_maximum,
        binding.initial_backoff_ms,
        binding.max_backoff_ms,
        binding.max_delivery_attempts,
        _hash("gregale-event-ack-intent-budget-adoption-v1\n" + body),
    )


def _validate_status(status: EventAcknowledgmentIntentStatus, request: _BudgetAdoptionRequest) -> None:
    policy, limit = status.retry_policy, status.delivery_attempt_limit
    if (
        (status.invocation_id, status.replay_generation, status.attempt)
        != (request.invocation_id, request.generation, request.attempt)
        or status.state not in ("pending", "unconfirmed")
        or status.last_code not in ("", "unconfirmed")
        or status.delivery_attempts != request.expected_delivery_attempts
        or limit != request.total
        or status.delivery_attempts >= limit
        or policy is None
        or policy.version != 2
        or policy.initial_backoff_ms != request.initial
        or policy.max_backoff_ms != request.maximum
        or policy.max_delivery_attempts != request.total
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
            "next_attempt_at": status.next_attempt_at,
            **({"claim_expires_at": status.claim_expires_at} if status.claim_expires_at is not None else {}),
            **({"last_code": status.last_code} if status.last_code else {}),
            "created_at": status.created_at,
            "updated_at": status.updated_at,
            "retry_policy": {
                "version": 2,
                "initial_backoff_ms": policy.initial_backoff_ms,
                "max_backoff_ms": policy.max_backoff_ms,
                "max_delivery_attempts": policy.max_delivery_attempts,
            },
            "delivery_attempt_limit": status.delivery_attempt_limit,
        },
        separators=(",", ":"),
        allow_nan=False,
    )


def _receipt(
    row: Any, request: _BudgetAdoptionRequest, *, replay: bool
) -> EventAcknowledgmentIntentBudgetAdoptionReceipt:
    if not isinstance(row, (tuple, list)) or len(row) != 8:
        raise _store_error()
    command, invocation, generation, attempt, outcome, recorded, body, digest = row
    if (
        _uuid(command) != command
        or _uuid(invocation) != invocation
        or outcome not in ("adopted", "conflict", "not_found")
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
            raise EventAcknowledgmentIntentBudgetAdoptionError("event_acknowledgment_intent_conflict")
        raise _store_error()
    status = None
    if outcome == "adopted":
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
    return EventAcknowledgmentIntentBudgetAdoptionReceipt(
        command, invocation, generation, attempt, outcome, recorded, status
    )
