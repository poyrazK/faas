"""Portable Go-compatible outbox requests, policies and payload-free metadata."""

from __future__ import annotations

import calendar
import hashlib
import json
import math
import re
from dataclasses import asdict, dataclass
from datetime import datetime
from typing import Any, Literal

from .event_contract import EventContract
from .event_pipeline_state import event_pipeline_scope
from .pipeline_state import PipelineScope

DAY_MS = 86400000


class EventOutboxError(RuntimeError):
    def __init__(self, code: str):
        self.code = code
        super().__init__("SDK event outbox stopped: " + code)


def _scope_value(value: PipelineScope) -> PipelineScope:
    try:
        return event_pipeline_scope(value)
    except Exception:
        raise EventOutboxError("scope_mismatch") from None


def _integer(value: Any, low: int, high: int) -> int:
    if isinstance(value, str) and re.fullmatch(r"0|[1-9][0-9]*", value):
        value = int(value)
    if type(value) is not int or not low <= value <= high:
        raise EventOutboxError("invalid_request")
    return value


def _milliseconds(value: float, low: int = 1, high: int = 2147483647) -> int:
    if type(value) not in (int, float) or not math.isfinite(value):
        raise EventOutboxError("invalid_request")
    milliseconds = round(value * 1000)
    if not math.isclose(value * 1000, milliseconds, abs_tol=0.000001, rel_tol=0):
        raise EventOutboxError("invalid_request")
    return _integer(milliseconds, low, high)


def _key(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[!-~]{1,256}", value) is not None


def _slug(value: Any) -> bool:
    return (
        isinstance(value, str)
        and 0 < len(value.encode("utf-8")) <= 256
        and value.strip() == value
        and not any(c in value for c in "/\\?#\0")
    )


def _hash(value: str | bytes) -> bytes:
    return hashlib.sha256(value.encode("utf-8") if isinstance(value, str) else value).digest()


def _instant(value: Any) -> int:
    try:
        if not isinstance(value, str):
            raise ValueError
        match = re.fullmatch(r"(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})", value)
        if match is None:
            raise ValueError
        stamp = datetime.fromisoformat(match[1] + match[3].replace("Z", "+00:00"))
        return calendar.timegm(stamp.utctimetuple()) * 1000000000 + int((match[2] or "").ljust(9, "0"))
    except (ValueError, TypeError, OverflowError):
        raise EventOutboxError("invalid_request") from None


@dataclass(frozen=True, kw_only=True)
class EventPublicationOptions:
    key: str
    time: str | None = None
    schema_version: str | None = None
    environment: str | None = None


@dataclass(frozen=True, kw_only=True)
class EventOutboxRetryPolicy:
    initial_backoff: float = 5
    max_backoff: float = 300
    max_attempts: int = 0

    def milliseconds(self) -> tuple[int, int, int]:
        initial = _milliseconds(self.initial_backoff, 1, 3600000)
        return initial, _milliseconds(self.max_backoff, initial, 3600000), _integer(self.max_attempts, 0, 2147483647)


def _policy_digest(policy: EventOutboxRetryPolicy) -> bytes:
    return _hash("gregale-event-outbox-policy-v1\n" + json.dumps([1, *policy.milliseconds()], separators=(",", ":")))


def event_outbox_retry_delay(
    token: bytes, attempts: int, policy: EventOutboxRetryPolicy | None, backoff_ms: int
) -> int:
    if policy is None:
        return backoff_ms
    initial, maximum, _ = policy.milliseconds()
    cap = min(maximum, initial * (1 << min(max(attempts - 1, 0), 32)))
    return int.from_bytes(_hash(b"gregale-event-outbox-jitter-v1\n" + token)[:8], "big") % cap + 1


@dataclass(frozen=True, kw_only=True)
class EventOutboxStatus:
    producer_key: str
    app_slug: str
    state: Literal["pending", "unconfirmed", "published", "blocked"]
    attempts: int
    next_attempt_at: str
    retry_until: str
    claim_expires_at: str | None
    accepted_at: str | None
    last_code: str
    created_at: str
    updated_at: str

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)

    @classmethod
    def from_dict(cls, value: Any) -> EventOutboxStatus:
        try:
            keys = cls.__dataclass_fields__
            r = {key: value[key] for key in keys}
            if (
                not _key(r["producer_key"])
                or not _slug(r["app_slug"])
                or r["state"] not in ("pending", "unconfirmed", "published", "blocked")
            ):
                raise ValueError
            if r["last_code"] not in (
                "",
                "unconfirmed",
                "publication_confirmed",
                "publication_rejected",
                "retry_window_expired",
                "attempts_exhausted",
            ):
                raise ValueError
            r["attempts"] = _integer(r["attempts"], 0, 2147483647)
            for key in ("next_attempt_at", "retry_until", "created_at", "updated_at"):
                _instant(r[key])
            for key in ("claim_expires_at", "accepted_at"):
                if r[key] is not None:
                    _instant(r[key])
            if (r["state"] == "published") != (r["accepted_at"] is not None) or _instant(r["retry_until"]) <= _instant(
                r["created_at"]
            ):
                raise ValueError
            return cls(**r)
        except Exception:
            raise EventOutboxError("store_failed") from None


@dataclass(frozen=True, kw_only=True)
class EventPublicationReceipt:
    app_id: str
    source: str
    event_id: str
    account_id: str
    accepted_at: str
    duplicate: bool


@dataclass(frozen=True, kw_only=True)
class EventPublicationOutcome:
    state: Literal["published", "unconfirmed", "blocked"]
    receipt: EventPublicationReceipt | None = None
    code: str | None = None
    http_status: int | None = None


@dataclass(frozen=True, kw_only=True)
class EventOutboxOutcome:
    outcome: Literal["published", "unconfirmed", "blocked", "empty", "paused", "rate_limited", "claim_lost"]
    status: EventOutboxStatus | None = None
    retry_after_ms: int | None = None
    retry_at: str | None = None

    def to_dict(self) -> dict[str, Any]:
        return {k: v for k, v in asdict(self).items() if v is not None}


def _parse_request(body: str, app: str, key: str | None = None) -> dict[str, Any]:
    try:
        if not isinstance(body, str) or not 0 < len(body.encode("utf-8")) <= 1048576:
            raise ValueError
        value = json.loads(body, parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
        if not isinstance(value, dict) or set(value) - {
            "expected_app_id",
            "key",
            "type",
            "data",
            "time",
            "schemaversion",
            "environment",
        }:
            raise ValueError
        if value.get("expected_app_id") != app or not _key(value.get("key")) or key is not None and value["key"] != key:
            raise ValueError
        if (
            not isinstance(value.get("type"), str)
            or not value["type"].strip()
            or len(value["type"].encode("utf-8")) > 256
            or "data" not in value
        ):
            raise ValueError
        if "time" in value:
            _instant(value["time"])
        for field, maximum in (("schemaversion", 64), ("environment", 40)):
            if field in value and (not isinstance(value[field], str) or len(value[field].encode("utf-8")) > maximum):
                raise ValueError
        if "environment" in value and (
            value["environment"].strip() != value["environment"] or any(c in value["environment"] for c in "\r\n\0")
        ):
            raise ValueError
        return value
    except Exception:
        raise EventOutboxError("invalid_request") from None


def prepare_event_publication(
    contract: EventContract[Any], scope: PipelineScope, data: Any, options: EventPublicationOptions
) -> str:
    try:
        if (
            options.schema_version is not None
            and not isinstance(options.schema_version, str)
            or options.environment is not None
            and not isinstance(options.environment, str)
        ):
            raise ValueError
        value = {
            "expected_app_id": scope.app_id,
            "key": options.key,
            "type": contract.spec["type"],
            "data": contract.input(data),
        }
        if options.time is not None:
            value["time"] = options.time
        if options.schema_version:
            value["schemaversion"] = options.schema_version
        if options.environment:
            value["environment"] = options.environment
        body = json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
        for character in "<>&\u2028\u2029":
            body = body.replace(character, "\\u" + format(ord(character), "04x"))
        _parse_request(body, scope.app_id)
        return body
    except Exception:
        raise EventOutboxError("invalid_request") from None


def _receipt(value: Any, scope: PipelineScope, key: str) -> EventPublicationReceipt:
    try:
        r = value["receipt"]
        if (
            value["app_id"] != scope.app_id
            or value["source"] != "app." + scope.app_id
            or type(value["duplicate"]) is not bool
            or r["account_id"] != scope.account_id
            or r["id"] != "key." + _hash(key).hex()
            or _instant(r["accepted_at"]) == -62135596800000000000
        ):
            raise ValueError
        return EventPublicationReceipt(
            app_id=scope.app_id,
            source=value["source"],
            account_id=scope.account_id,
            event_id=r["id"],
            accepted_at=r["accepted_at"],
            duplicate=value["duplicate"],
        )
    except Exception:
        raise EventOutboxError("unverified") from None
