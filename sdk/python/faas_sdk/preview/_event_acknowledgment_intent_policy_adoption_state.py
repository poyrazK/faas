"""Private v1 adoption framing and metadata shared with Go and Node."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, replace
from datetime import UTC, datetime
from typing import Any

from .event_acknowledgment_intent_retry_policy import EventAcknowledgmentIntentRetryPolicy, _retry_schedule
from .event_acknowledgment_intents import (
    EventAcknowledgmentIntentError,
    EventAcknowledgmentIntentStatus,
    _bytes,
    _hash,
    _instant,
    _integer,
    _status,
    _store_error,
)
from .event_consumer_transactions import _uuid


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentAdoptRetryPolicyOptions:
    """Save these original inputs and command UUID durably before issuing adoption."""

    command_id: str
    expected_updated_at: str
    expected_delivery_attempts: int
    retry_policy: EventAcknowledgmentIntentRetryPolicy | None = None


@dataclass(frozen=True, slots=True)
class EventAcknowledgmentIntentPolicyAdoptionReceipt:
    """Historical local metadata only; no callback capability, result or claim."""

    command_id: str
    invocation_id: str
    replay_generation: int
    attempt: int
    outcome: str
    recorded_at: str
    intent: EventAcknowledgmentIntentStatus | None = None


class EventAcknowledgmentIntentPolicyAdoptionError(EventAcknowledgmentIntentError):
    """Guard rejections have a receipt only after known commit or retained replay."""

    def __init__(self, code: str, receipt: EventAcknowledgmentIntentPolicyAdoptionReceipt | None = None):
        if code not in ("event_acknowledgment_intent_conflict", "event_acknowledgment_intent_not_found"):
            raise ValueError("invalid acknowledgment policy adoption rejection")
        super().__init__(code)
        self.receipt = receipt


@dataclass(frozen=True, slots=True)
class _AdoptionRequest:
    command_id: str
    invocation_id: str
    generation: int
    attempt: int
    expected_updated_at: str
    expected_delivery_attempts: int
    initial: int
    maximum: int
    digest: bytes


def _at(raw: Any) -> datetime:
    if (
        type(raw) is not str
        or re.fullmatch(
            r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,6})?(?:Z|[+-][0-9]{2}:[0-9]{2})",
            raw,
        )
        is None
    ):
        raise ValueError("invalid acknowledgment policy adoption timestamp")
    # fromisoformat preserves microseconds; explicit validation rejects offsets
    # with hours/minutes beyond RFC3339 instead of normalizing them.
    if raw[-1] != "Z" and (int(raw[-5:-3]) > 23 or int(raw[-2:]) > 59):
        raise ValueError("invalid acknowledgment policy adoption timestamp")
    value = datetime.fromisoformat(raw.replace("Z", "+00:00")).astimezone(UTC)
    if value == datetime.min.replace(tzinfo=UTC):
        raise ValueError("invalid acknowledgment policy adoption timestamp")
    return value


def _canonical_time(raw: Any) -> str:
    text = _at(raw).isoformat(timespec="microseconds").replace("+00:00", "Z")
    return text[:-1].rstrip("0").rstrip(".") + "Z"


def _request(
    invocation: str, generation: int, attempt: int, options: EventAcknowledgmentIntentAdoptRetryPolicyOptions
) -> _AdoptionRequest:
    if (
        type(options) is not EventAcknowledgmentIntentAdoptRetryPolicyOptions
        or type(generation) is not int
        or not 0 <= generation <= 9223372036854775807
        or type(attempt) is not int
        or not 1 <= attempt <= 2147483647
        or type(options.expected_delivery_attempts) is not int
        or not 0 <= options.expected_delivery_attempts <= 2147483647
    ):
        raise ValueError("invalid acknowledgment policy adoption options")
    command, invocation = _uuid(options.command_id), _uuid(invocation)
    expected = _canonical_time(options.expected_updated_at)
    policy = _retry_schedule(
        None, EventAcknowledgmentIntentRetryPolicy() if options.retry_policy is None else options.retry_policy
    ).retry_policy
    if policy is None:
        raise ValueError("invalid acknowledgment policy adoption options")
    # Fixed field order/compact separators match Go's v1 digest framing. Native
    # integers preserve all int64 generations; never convert through float.
    body = json.dumps(
        {
            "version": 1,
            "action": "adopt_retry_policy",
            "invocation_id": invocation,
            "replay_generation": generation,
            "attempt": attempt,
            "expected_updated_at": expected,
            "expected_delivery_attempts": options.expected_delivery_attempts,
            "policy_version": 1,
            "initial_backoff_ms": policy.initial_backoff_ms,
            "max_backoff_ms": policy.max_backoff_ms,
        },
        separators=(",", ":"),
        allow_nan=False,
    )
    return _AdoptionRequest(
        command,
        invocation,
        generation,
        attempt,
        expected,
        options.expected_delivery_attempts,
        policy.initial_backoff_ms,
        policy.max_backoff_ms,
        _hash("gregale-event-ack-intent-policy-adoption-v1\n" + body),
    )


def _adoption_status(row: Any) -> EventAcknowledgmentIntentStatus:
    status = _status(row)
    return replace(
        status,
        next_attempt_at=_canonical_time(status.next_attempt_at),
        claim_expires_at=None if status.claim_expires_at is None else _canonical_time(status.claim_expires_at),
        created_at=_canonical_time(status.created_at),
        updated_at=_canonical_time(status.updated_at),
    )


def _validate_status(status: EventAcknowledgmentIntentStatus, request: _AdoptionRequest) -> None:
    policy = status.retry_policy
    if (
        (status.invocation_id, status.replay_generation, status.attempt)
        != (request.invocation_id, request.generation, request.attempt)
        or status.state not in ("pending", "unconfirmed")
        or status.last_code not in ("", "unconfirmed")
        or status.delivery_attempts != request.expected_delivery_attempts
        or _at(status.updated_at) <= _at(request.expected_updated_at)
        or policy is None
        or policy.version != 1
        or policy.max_delivery_attempts is not None
        or status.delivery_attempt_limit is not None
        or (policy.initial_backoff_ms, policy.max_backoff_ms) != (request.initial, request.maximum)
    ):
        raise _store_error()


def _snapshot(status: EventAcknowledgmentIntentStatus) -> str:
    policy = status.retry_policy
    if policy is None:
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
                "version": 1,
                "initial_backoff_ms": policy.initial_backoff_ms,
                "max_backoff_ms": policy.max_backoff_ms,
            },
        },
        separators=(",", ":"),
        allow_nan=False,
    )


def _invalid_constant(_value: str) -> Any:
    raise _store_error()


def _receipt(row: Any, request: _AdoptionRequest, *, replay: bool) -> EventAcknowledgmentIntentPolicyAdoptionReceipt:
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
            raise EventAcknowledgmentIntentPolicyAdoptionError("event_acknowledgment_intent_conflict")
        raise _store_error()
    status = None
    if outcome == "adopted":
        if type(body) is not str or not 0 < len(body.encode("utf-8")) <= 8192:
            raise _store_error()
        saved = json.loads(body, parse_constant=_invalid_constant)
        if type(saved) is not dict or type(saved.get("retry_policy")) is not dict:
            raise _store_error()
        policy = saved["retry_policy"]
        if (
            saved.get("delivery_attempt_limit") is not None
            or policy.get("max_delivery_attempts") is not None
            or type(policy.get("version")) is not int
            or policy["version"] != 1
        ):
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
                None,
                None,
            )
        )
        _validate_status(status, request)
    elif body is not None:
        raise _store_error()
    return EventAcknowledgmentIntentPolicyAdoptionReceipt(
        command, invocation, generation, attempt, outcome, recorded, status
    )
