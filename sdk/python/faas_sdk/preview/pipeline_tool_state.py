"""Portable application-owned pipeline tool receipts, without inputs or credentials."""

from __future__ import annotations

import copy
import json
import re
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any, Literal, Protocol

from ..job_runtime import job_value_hash
from ._pipeline_tool_protocol import SQL
from .event_pipeline_state import event_pipeline_scope
from .job_pipeline_client import PipelineCheckpoint
from .pipeline_state import PipelineScope, _hash, _uuid, pipeline_name

PipelineToolAction = Literal["start", "inspect", "result", "cancel", "reconcile"]


class PipelineToolError(RuntimeError):
    def __init__(self, code: str):
        self.code = code
        super().__init__("Pipeline tool: " + code)


@dataclass(frozen=True, kw_only=True)
class PipelineToolActor:
    subject_id: str
    owner_id: str


@dataclass(frozen=True, kw_only=True)
class PipelineToolReceipt:
    intent: dict[str, Any]
    checkpoint: PipelineCheckpoint | None

    def to_dict(self) -> dict[str, Any]:
        return {
            "intent": copy.deepcopy(self.intent),
            "checkpoint": self.checkpoint.to_dict() if self.checkpoint else None,
        }

    @classmethod
    def from_dict(cls, raw: Any) -> PipelineToolReceipt:
        try:
            if not isinstance(raw, dict) or set(raw) != {"intent", "checkpoint"}:
                raise ValueError
            i = raw["intent"]
            if (
                set(i)
                != {
                    "version",
                    "identity",
                    "scope",
                    "name",
                    "request_id",
                    "subject_id",
                    "owner_id",
                    "contract_sha256",
                    "input_sha256",
                    "idempotency_key",
                }
                or type(i["version"]) is not int
                or i["version"] != 1
                or not pipeline_name(i["owner_id"])
                or not _hash(i["contract_sha256"])
                or not _hash(i["input_sha256"])
            ):
                raise ValueError
            scope = event_pipeline_scope(PipelineScope.from_dict(i["scope"]))
            if (
                job_value_hash(scope.to_dict()) != job_value_hash(i["scope"])
                or i["identity"] != identity(scope, i["name"], i["subject_id"], i["request_id"])
                or i["idempotency_key"] != "pipeline-tool:" + i["identity"]
            ):
                raise ValueError
            checkpoint = PipelineCheckpoint.from_dict(raw["checkpoint"]) if raw["checkpoint"] is not None else None
            if checkpoint is not None:
                match_checkpoint(i, checkpoint)
            return cls(intent=copy.deepcopy(i), checkpoint=checkpoint)
        except Exception:
            raise PipelineToolError("receipt_conflict") from None


def tool_name(v: Any) -> bool:
    return isinstance(v, str) and re.fullmatch(r"[a-z][a-z0-9_]{0,47}", v) is not None


def request_id(v: Any) -> str:
    if not _uuid(v):
        raise PipelineToolError("invalid_input")
    return str(v)


def identity(scope: PipelineScope, name: str, subject: str, id: str) -> str:
    if not tool_name(name) or not pipeline_name(subject):
        raise PipelineToolError("invalid_input")
    return job_value_hash(
        ["gregale.pipeline-tool.v1", event_pipeline_scope(scope).to_dict(), name, subject, request_id(id)]
    )


def match_checkpoint(intent: dict[str, Any], c: PipelineCheckpoint) -> PipelineCheckpoint:
    checked = PipelineCheckpoint.from_dict(c.to_dict())
    if (
        checked.owner_id != intent["owner_id"]
        or checked.contract_sha256 != intent["contract_sha256"]
        or checked.input_sha256 != intent["input_sha256"]
        or job_value_hash(checked.scope.to_dict()) != job_value_hash(intent["scope"])
    ):
        raise PipelineToolError("receipt_conflict")
    return checked


class PipelineToolStore(Protocol):
    async def load(self, identity: str) -> PipelineToolReceipt | None: ...
    async def reserve(self, intent: dict[str, Any]) -> tuple[bool, PipelineToolReceipt]: ...
    async def complete(self, intent: dict[str, Any], checkpoint: PipelineCheckpoint) -> PipelineToolReceipt: ...


class PostgresPipelineToolStore:
    """Every query is one acknowledged autocommit statement on the same primary."""

    def __init__(self, query: Callable[[str, list[Any]], Awaitable[list[dict[str, Any]]]]):
        self._query = query

    async def load(self, identity: str) -> PipelineToolReceipt | None:
        rows = await self._query(SQL["load"], [identity])
        if len(rows) > 1:
            raise PipelineToolError("receipt_conflict")
        return PipelineToolReceipt.from_dict(rows[0]["receipt"]) if rows else None

    async def reserve(self, intent: dict[str, Any]) -> tuple[bool, PipelineToolReceipt]:
        receipt = PipelineToolReceipt.from_dict(dict(intent=intent, checkpoint=None))
        rows = await self._query(
            SQL["reserve"],
            [intent["identity"], json.dumps(receipt.to_dict(), ensure_ascii=False, separators=(",", ":"))],
        )
        retained = (
            PipelineToolReceipt.from_dict(rows[0]["receipt"]) if len(rows) == 1 else await self.load(intent["identity"])
        )
        if len(rows) > 1 or retained is None or job_value_hash(retained.intent) != job_value_hash(intent):
            raise PipelineToolError("receipt_conflict")
        return len(rows) == 1, retained

    async def complete(self, intent: dict[str, Any], checkpoint: PipelineCheckpoint) -> PipelineToolReceipt:
        match_checkpoint(intent, checkpoint)
        rows = await self._query(
            SQL["complete"], [intent["identity"], json.dumps(intent), json.dumps(checkpoint.to_dict())]
        )
        if len(rows) != 1:
            raise PipelineToolError("receipt_conflict")
        receipt = PipelineToolReceipt.from_dict(rows[0]["receipt"])
        if job_value_hash(receipt.intent) != job_value_hash(intent) or receipt.checkpoint != checkpoint:
            raise PipelineToolError("receipt_conflict")
        return receipt
