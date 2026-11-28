"""Portable, application-owned event-to-workflow start receipts."""

from __future__ import annotations

import json
import re
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any, Protocol

from ..job_runtime import job_value_hash
from .event_consumer_transactions import _uuid as consumer_uuid
from .event_consumer_transactions import new_event_consumer_request
from .event_contract import _event_type
from .job_pipeline_client import PipelineCheckpoint
from .pipeline_state import PipelineScope, _hash, pipeline_name


class EventPipelineError(RuntimeError):
    def __init__(self, code: str):
        self.code = code
        super().__init__("SDK event pipeline stopped: " + code)


def event_pipeline_scope(value: PipelineScope) -> PipelineScope:
    try:
        return PipelineScope.from_dict(
            value.to_dict()
            | {
                "account_id": consumer_uuid(value.account_id),
                "app_id": consumer_uuid(value.app_id),
            }
        )
    except (ValueError, TypeError, AttributeError):
        raise EventPipelineError("scope_mismatch") from None


def event_pipeline_identity(scope: PipelineScope, name: str, subscription_id: str, acceptance_id: str) -> str:
    if (
        not pipeline_name(name)
        or not isinstance(acceptance_id, str)
        or not re.fullmatch(r"[1-9][0-9]{0,18}", acceptance_id)
        or int(acceptance_id) > 9223372036854775807
    ):
        raise EventPipelineError("invalid_event")
    try:
        return job_value_hash(
            [
                "gregale.event-pipeline.v1",
                event_pipeline_scope(scope).to_dict(),
                name,
                consumer_uuid(subscription_id),
                acceptance_id,
            ]
        )
    except (ValueError, TypeError, AttributeError):
        raise EventPipelineError("invalid_event") from None


def match_event_pipeline_checkpoint(intent: dict[str, Any], checkpoint: PipelineCheckpoint) -> PipelineCheckpoint:
    try:
        c = PipelineCheckpoint.from_dict(checkpoint.to_dict())
        if (
            c.scope.to_dict() != intent["scope"]
            or c.owner_id != intent["owner_id"]
            or c.contract_sha256 != intent["pipeline_sha256"]
            or c.input_sha256 != intent["input_sha256"]
        ):
            raise ValueError
        return c
    except Exception:
        raise EventPipelineError("receipt_conflict") from None


@dataclass(frozen=True, kw_only=True)
class EventPipelineReceipt:
    _intent_json: str
    checkpoint: PipelineCheckpoint | None

    @property
    def intent(self) -> dict[str, Any]:
        return json.loads(self._intent_json)  # type: ignore[no-any-return]

    def to_dict(self) -> dict[str, Any]:
        return {"intent": self.intent, "checkpoint": self.checkpoint.to_dict() if self.checkpoint is not None else None}

    @classmethod
    def from_dict(cls, value: Any) -> EventPipelineReceipt:
        try:
            if not isinstance(value, dict) or set(value) != {"intent", "checkpoint"}:
                raise ValueError
            i = value["intent"]
            if not isinstance(i, dict) or set(i) != {
                "version",
                "identity",
                "scope",
                "name",
                "subscription_id",
                "acceptance_id",
                "source",
                "event_id",
                "event_type",
                "body_sha256",
                "event_contract_sha256",
                "pipeline_sha256",
                "owner_id",
                "input_sha256",
                "idempotency_key",
            }:
                raise ValueError
            scope = event_pipeline_scope(PipelineScope.from_dict(i["scope"]))
            if (
                type(i["version"]) is not int
                or i["version"] != 1
                or scope.to_dict() != i["scope"]
                or not pipeline_name(i["owner_id"])
                or not all(
                    _hash(i[k]) for k in ("body_sha256", "event_contract_sha256", "pipeline_sha256", "input_sha256")
                )
                or not _event_type(i["event_type"])
                or i["subscription_id"] != consumer_uuid(i["subscription_id"])
                or i["identity"] != event_pipeline_identity(scope, i["name"], i["subscription_id"], i["acceptance_id"])
                or i["idempotency_key"] != "event-pipeline:" + i["identity"]
            ):
                raise ValueError
            new_event_consumer_request(
                scope.account_id,
                scope.app_id,
                i["subscription_id"],
                int(i["acceptance_id"]),
                i["source"],
                i["event_id"],
                json.dumps(
                    {
                        "specversion": "1.0",
                        "source": i["source"],
                        "id": i["event_id"],
                        "type": i["event_type"],
                        "data": None,
                    }
                ).encode(),
            )
            checkpoint = PipelineCheckpoint.from_dict(value["checkpoint"]) if value["checkpoint"] is not None else None
            if checkpoint is not None:
                match_event_pipeline_checkpoint(i, checkpoint)
            return cls(_intent_json=json.dumps(i, ensure_ascii=False, separators=(",", ":")), checkpoint=checkpoint)
        except Exception:
            raise EventPipelineError("receipt_conflict") from None


class EventPipelineStore(Protocol):
    async def load(self, identity: str) -> EventPipelineReceipt | None: ...
    async def reserve(self, intent: dict[str, Any]) -> tuple[bool, EventPipelineReceipt]: ...
    async def complete(self, intent: dict[str, Any], checkpoint: PipelineCheckpoint) -> EventPipelineReceipt: ...


EVENT_PIPELINE_SCHEMA = """CREATE TABLE IF NOT EXISTS public.gregale_event_pipeline_starts (
 identity text PRIMARY KEY CHECK (identity ~ '^[a-f0-9]{64}$'),
 receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
"""


class PostgresEventPipelineStore:
    """Each query is an acknowledged autocommit statement on the same primary.

    PostgreSQL $n placeholders match Node. No ambient transaction, read replica,
    automatic DDL, pruning or lease takeover.
    """

    def __init__(self, query: Callable[[str, list[Any]], Awaitable[list[dict[str, Any]]]]):
        self._query = query

    async def load(self, identity: str) -> EventPipelineReceipt | None:
        rows = await self._query(
            "SELECT receipt FROM public.gregale_event_pipeline_starts WHERE identity=$1", [identity]
        )
        if len(rows) > 1:
            raise EventPipelineError("receipt_conflict")
        return EventPipelineReceipt.from_dict(rows[0]["receipt"]) if rows else None

    async def reserve(self, intent: dict[str, Any]) -> tuple[bool, EventPipelineReceipt]:
        EventPipelineReceipt.from_dict({"intent": intent, "checkpoint": None})
        rows = await self._query(
            "INSERT INTO public.gregale_event_pipeline_starts(identity,receipt) VALUES ($1,$2::jsonb) ON CONFLICT (identity) DO NOTHING RETURNING receipt",
            [intent["identity"], json.dumps({"intent": intent, "checkpoint": None})],
        )
        receipt = (
            EventPipelineReceipt.from_dict(rows[0]["receipt"])
            if len(rows) == 1
            else await self.load(intent["identity"])
        )
        if len(rows) > 1 or receipt is None or job_value_hash(receipt.intent) != job_value_hash(intent):
            raise EventPipelineError("receipt_conflict")
        return len(rows) == 1, receipt

    async def complete(self, intent: dict[str, Any], checkpoint: PipelineCheckpoint) -> EventPipelineReceipt:
        match_event_pipeline_checkpoint(intent, checkpoint)
        EventPipelineReceipt.from_dict({"intent": intent, "checkpoint": checkpoint.to_dict()})
        rows = await self._query(
            "UPDATE public.gregale_event_pipeline_starts SET receipt=jsonb_set(receipt,'{checkpoint}',$3::jsonb) WHERE identity=$1 AND receipt->'intent'=$2::jsonb AND (receipt->'checkpoint'='null'::jsonb OR receipt->'checkpoint'=$3::jsonb) RETURNING receipt",
            [intent["identity"], json.dumps(intent), json.dumps(checkpoint.to_dict())],
        )
        if len(rows) != 1:
            raise EventPipelineError("receipt_conflict")
        receipt = EventPipelineReceipt.from_dict(rows[0]["receipt"])
        if job_value_hash(receipt.intent) != job_value_hash(intent) or receipt.checkpoint != checkpoint:
            raise EventPipelineError("receipt_conflict")
        return receipt
