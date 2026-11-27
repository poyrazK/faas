"""Application-owned grants for one authenticated clarification."""

from __future__ import annotations

import hmac
import re
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import TYPE_CHECKING, Any, Literal, NotRequired, Protocol, TypedDict, cast

from ..job_runtime import job_value_hash
from ._pipeline_tool_protocol import PIPELINE_TASK_HANDOFF_SCHEMA as PIPELINE_TASK_HANDOFF_SCHEMA
from ._pipeline_tool_protocol import SQL
from .pipeline_state import _hash, _uuid
from .query_tools import QueryToolError

if TYPE_CHECKING:
    from .pipeline_task_input import PipelineTaskFormRequest


class PipelineTaskHandoff(TypedDict):
    handoff_id: str
    expires_at: str


class PipelineTaskHandoffView(PipelineTaskHandoff):
    request_key: str
    form: PipelineTaskFormRequest


class PipelineTaskHandoffSummary(PipelineTaskHandoff):
    question: str
    kind: NotRequired[Literal["approval"]]


class PipelineTaskHandoffPage(TypedDict):
    items: list[PipelineTaskHandoffSummary]
    next_cursor: str | None


class PipelineTaskHandoffSnapshot(TypedDict):
    items: list[PipelineTaskHandoffSummary]
    truncated: bool


class PipelineTaskHandoffRecord(TypedDict):
    id: str
    issuer_hash: str
    reviewer_hash: str
    task_id: str
    tool: str
    run_id: str
    gate_id: str
    binding_sha256: str
    expires_at: str


class PipelineTaskHandoffCandidate(TypedDict):
    position: str
    grant: PipelineTaskHandoffRecord


class PipelineGateHandoff(TypedDict):
    namespace: str
    id: str
    reviewer_hash: str
    run_id: str


class PipelineTaskHandoffStore(Protocol):
    async def grant(self, namespace: str, record: PipelineTaskHandoffRecord) -> PipelineTaskHandoffRecord: ...
    async def load(self, namespace: str, id: str, reviewer: str) -> PipelineTaskHandoffRecord | None: ...
    async def revoke(self, namespace: str, id: str, issuer: str) -> bool: ...


@dataclass(frozen=True, kw_only=True)
class PipelineTaskHandoffSettings:
    store: PipelineTaskHandoffStore
    scopes: Sequence[str]


def _reviewer(key: bytes, namespace: str, resource: str, subject: str) -> str:
    digest = job_value_hash(["gregale.pipeline-task-handoff.v1", namespace, resource, subject])
    return hmac.digest(key, digest.encode(), "sha256").hex()


_POSITION = re.compile(r"[0-9a-f]{32}")
_CURSOR = re.compile(r"[0-9a-f]{32}\.[0-9a-f]{64}")
_PAGE_LIMIT = 25
_SCAN_LIMIT = 64


def _cursor(key: bytes, reviewer: str, position: str) -> str:
    digest = job_value_hash(["gregale.pipeline-task-handoff-list.v1", reviewer, position])
    return position + "." + hmac.digest(key, digest.encode(), "sha256").hex()


def _after(key: bytes, reviewer: str, cursor: Any) -> str:
    if not isinstance(cursor, str) or not _CURSOR.fullmatch(cursor):
        raise QueryToolError("invalid_input")
    position = cursor[:32]
    if not hmac.compare_digest(cursor, _cursor(key, reviewer, position)):
        raise QueryToolError("invalid_input")
    return position


def _approval_key(key: bytes, row: PipelineTaskHandoffRecord, review_digest: str) -> str:
    digest = job_value_hash(["gregale.pipeline-task-approval.v1", row["id"], row["reviewer_hash"],
                             row["gate_id"], row["binding_sha256"], review_digest])
    return hmac.digest(key, digest.encode(), "sha256").hex()


def _parse_handoff(value: Any) -> PipelineTaskHandoffRecord:
    if (
        not isinstance(value, dict)
        or set(value) != {"id", "issuer_hash", "reviewer_hash", "task_id", "tool", "run_id", "gate_id", "binding_sha256", "expires_at"}
        or not all(_uuid(value[name]) for name in ("id", "task_id", "run_id"))
        or not all(_hash(value[name]) for name in ("issuer_hash", "reviewer_hash", "gate_id", "binding_sha256"))
        or not isinstance(value["tool"], str)
        or not re.fullmatch(r"[a-z][a-z0-9_]{0,63}", value["tool"])
        or not isinstance(value["expires_at"], str)
    ):
        raise QueryToolError("invalid_output")
    try:
        if datetime.fromisoformat(value["expires_at"]).tzinfo is None:
            raise ValueError
    except (ValueError, OverflowError):
        raise QueryToolError("invalid_output") from None
    return cast(PipelineTaskHandoffRecord, dict(value))


class PostgresPipelineTaskHandoffStore:
    """Use the same PostgreSQL primary as the pipeline gate decision store."""

    def __init__(self, query: Callable[[str, list[Any]], Awaitable[Sequence[dict[str, Any]]]]):
        self._query = query

    async def _one(self, key: str, args: list[Any]) -> dict[str, Any] | None:
        rows = await self._query(SQL[key], args)
        if len(rows) > 1:
            raise QueryToolError("invalid_output")
        return rows[0] if rows else None

    async def grant(self, namespace: str, record: PipelineTaskHandoffRecord) -> PipelineTaskHandoffRecord:
        r = _parse_handoff(record)
        return _parse_handoff(await self._one("task_handoff_grant", [
            namespace, r["id"], r["issuer_hash"], r["reviewer_hash"], r["task_id"], r["tool"],
            r["run_id"], r["gate_id"], r["binding_sha256"], r["expires_at"],
        ]))

    async def load(self, namespace: str, id: str, reviewer: str) -> PipelineTaskHandoffRecord | None:
        raw = await self._one("task_handoff_read", [namespace, id, reviewer])
        return _parse_handoff(raw) if raw is not None else None

    async def scan(self, namespace: str, reviewer: str, after: str, limit: int) -> list[PipelineTaskHandoffCandidate]:
        candidates = []
        for raw in await self._query(SQL["task_handoff_scan"], [namespace, reviewer, after, limit]):
            grant = dict(raw)
            position = grant.pop("position", None)
            if not isinstance(position, str) or not _POSITION.fullmatch(position):
                raise QueryToolError("invalid_output")
            candidates.append(PipelineTaskHandoffCandidate(position=position, grant=_parse_handoff(grant)))
        return candidates

    async def revoke(self, namespace: str, id: str, issuer: str) -> bool:
        raw = await self._one("task_handoff_revoke", [namespace, id, issuer])
        return raw is not None and raw.get("id") == id
