"""Verified native Stripe snapshot context and payload-free handoff metadata."""

from __future__ import annotations

import copy
import hashlib
import json
import math
import re
from dataclasses import asdict, dataclass, field
from typing import Any, Generic, Literal, TypeVar
from uuid import NAMESPACE_URL, uuid5

from ..job_runtime import job_value_hash
from .event_consumer_transactions import _uuid as consumer_uuid
from .event_outbox_state import _instant
from .pipeline_state import PipelineScope, pipeline_name

E = TypeVar("E")
StripeWebhookIgnoreReason = Literal["not_applicable", "already_applied", "cancelled", "superseded"]


class StripeWebhookError(RuntimeError):
    def __init__(self, code: str, identity: str | None = None):
        self.code, self.identity = code, identity
        super().__init__("Stripe webhook: " + code)


@dataclass(frozen=True, kw_only=True)
class StripeWebhookDelivery:
    method: str
    path: str
    headers: dict[str, str | list[str] | tuple[str, ...] | None]
    body: bytes


@dataclass(frozen=True, kw_only=True)
class StripeDeliveryContext:
    account_id: str
    app_id: str
    endpoint_id: str
    receipt_id: str
    event_id: str
    event_type: str
    body_sha256: str


@dataclass(frozen=True, kw_only=True)
class StripeWebhookEvent:
    id: str
    type: str
    api_version: str
    livemode: bool
    created: int
    data_object: dict[str, Any]
    account: str | None = None


@dataclass(frozen=True, kw_only=True)
class StripeWebhookStage(Generic[E]):
    input: E
    state: Literal["stage"] = field(default="stage", init=False)


@dataclass(frozen=True, kw_only=True)
class StripeWebhookIgnore:
    reason: StripeWebhookIgnoreReason
    state: Literal["ignore"] = field(default="ignore", init=False)


StripeWebhookDecision = StripeWebhookStage[E] | StripeWebhookIgnore


@dataclass(frozen=True, kw_only=True)
class StripeWebhookReceipt:
    version: int
    identity: str
    endpoint_id: str
    event_id: str
    state: Literal["staged", "ignored"]
    owner_id: str
    outbox_key: str | None
    reason: str
    created_at: str
    updated_at: str

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass(frozen=True, kw_only=True)
class StripeWebhookOutcome:
    outcome: Literal["staged", "ignored"]
    replayed: bool
    identity: str
    receipt: StripeWebhookReceipt | None
    reason: str | None = None

    def to_dict(self) -> dict[str, Any]:
        return {k: v for k, v in asdict(self).items() if v is not None}


def digest(v: str | bytes) -> str:
    return hashlib.sha256(v.encode() if isinstance(v, str) else v).hexdigest()


def event_id(v: Any) -> bool:
    return isinstance(v, str) and len(v) <= 256 and re.fullmatch(r"evt_[A-Za-z0-9_-]+", v) is not None


def event_type(v: Any) -> bool:
    return isinstance(v, str) and re.fullmatch(r"[a-z][a-z0-9_.]{0,255}", v) is not None


def api_version(v: Any) -> bool:
    return isinstance(v, str) and re.fullmatch(r"\d{4}-\d{2}-\d{2}(?:\.[a-z][a-z0-9_-]{0,63})?", v) is not None


def path_valid(v: Any) -> bool:
    return (
        isinstance(v, str)
        and re.fullmatch(r"/[A-Za-z0-9_./~-]*", v) is not None
        and not v.startswith("//")
        and len(v) <= 256
        and not any(p in (".", "..") for p in v.split("/"))
    )


def receipt_id(endpoint: str, event: str) -> str:
    return str(uuid5(NAMESPACE_URL, "gregale:inbound-webhook:" + consumer_uuid(endpoint) + "\0" + event))


def capture(d: StripeWebhookDelivery) -> StripeWebhookDelivery:
    if d.method != "POST" or not path_valid(d.path) or not isinstance(d.body, bytes) or not 0 < len(d.body) <= 1048576:
        raise StripeWebhookError("invalid_delivery")
    return StripeWebhookDelivery(method="POST", path=d.path, headers=copy.deepcopy(d.headers), body=bytes(d.body))


def stripe_delivery_context_from_headers(
    headers: dict[str, Any], body: bytes, account_id: str, app_id: str
) -> StripeDeliveryContext:
    """Authenticate the platform boundary first. These headers are not signatures."""
    try:

        def read(name: str) -> str:
            rows = [v for k, v in headers.items() if k.lower() == name]
            if len(rows) != 1 or not isinstance(rows[0], str) or not rows[0]:
                raise ValueError
            return rows[0]

        endpoint = consumer_uuid(read("x-gregale-webhook-endpoint-id"))
        id = read("x-gregale-webhook-event-id")
        type = read("x-gregale-webhook-event-type")
        sha = read("x-gregale-webhook-body-sha256")
        if (
            read("x-gregale-webhook-provider") != "stripe"
            or not event_id(id)
            or not event_type(type)
            or sha != digest(body)
        ):
            raise ValueError
        original = receipt_id(endpoint, id)
        invocation = consumer_uuid(read("x-faas-invocation-id"))
        source = read("x-faas-invocation-source")
        if source == "inbound_webhook":
            if invocation != original:
                raise ValueError
        elif source == "replay":
            generation = read("x-faas-invocation-replay-generation")
            if (
                re.fullmatch(r"[1-9][0-9]{0,18}", generation) is None
                or int(generation) > 9223372036854775807
                or consumer_uuid(read("x-faas-invocation-replay-root")) != original
                or invocation == original
            ):
                raise ValueError
        else:
            raise ValueError
        return StripeDeliveryContext(
            account_id=consumer_uuid(account_id),
            app_id=consumer_uuid(app_id),
            endpoint_id=endpoint,
            receipt_id=original,
            event_id=id,
            event_type=type,
            body_sha256=sha,
        )
    except Exception:
        raise StripeWebhookError("invalid_delivery") from None


def _safe(v: Any) -> None:
    if type(v) in (int, float):
        if not math.isfinite(v) or abs(v) > 9007199254740991:
            raise ValueError
    elif isinstance(v, str):
        v.encode("utf-8")
    elif isinstance(v, list):
        for x in v:
            _safe(x)
    elif isinstance(v, dict):
        for k, x in v.items():
            _safe(k)
            _safe(x)


def snapshot(
    body: bytes, context: StripeDeliveryContext, version: str, live: bool, account: str | None
) -> StripeWebhookEvent:
    try:
        raw = json.loads(body.decode("utf-8"))
        _safe(raw)
        obj = raw.get("data", {}).get("object")
        if (
            raw.get("object") != "event"
            or not event_id(raw.get("id"))
            or not event_type(raw.get("type"))
            or raw["id"] != context.event_id
            or raw["type"] != context.event_type
            or type(raw.get("livemode")) is not bool
            or raw["livemode"] != live
            or raw.get("account") != account
            or type(raw.get("created")) not in (int, float)
            or int(raw["created"]) != raw["created"]
            or raw["created"] < 0
            or not isinstance(obj, dict)
            or not isinstance(obj.get("id"), str)
            or re.fullmatch(r"[A-Za-z0-9_-]{1,256}", obj["id"]) is None
            or not isinstance(obj.get("object"), str)
        ):
            raise ValueError
        if raw.get("api_version") != version:
            raise StripeWebhookError("unsupported_version")
        return StripeWebhookEvent(
            id=raw["id"],
            type=raw["type"],
            api_version=version,
            livemode=live,
            created=int(raw["created"]),
            account=account,
            data_object=copy.deepcopy(obj),
        )
    except StripeWebhookError:
        raise
    except Exception:
        raise StripeWebhookError("invalid_delivery") from None


def identity(scope: PipelineScope, endpoint: str, event: str) -> str:
    return job_value_hash(["gregale.stripe-webhook.v1", scope.to_dict(), endpoint, event])


def metadata(row: dict[str, Any], id: str, endpoint: str, event: str) -> StripeWebhookReceipt:
    try:
        if (
            row["identity"] != id
            or row["state"] not in ("staged", "ignored")
            or not pipeline_name(row["owner_id"])
            or row["state"] == "staged"
            and (row["outbox_key"] != "stripe-webhook:" + id or row["reason"] != "")
            or row["state"] == "ignored"
            and (
                row["outbox_key"] is not None
                or row["reason"] not in ("not_applicable", "already_applied", "cancelled", "superseded")
            )
        ):
            raise ValueError
        _instant(row["created_at"])
        _instant(row["updated_at"])
        return StripeWebhookReceipt(
            version=1,
            identity=id,
            endpoint_id=endpoint,
            event_id=event,
            **{k: row[k] for k in ("state", "owner_id", "outbox_key", "reason", "created_at", "updated_at")},
        )
    except Exception:
        raise StripeWebhookError("store_failed", id) from None
