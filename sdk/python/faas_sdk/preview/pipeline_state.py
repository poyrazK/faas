"""Portable pipeline ownership, durable job receipts and verified file handoff."""

from __future__ import annotations

import copy
import json
import re
from collections.abc import Awaitable, Callable
from dataclasses import asdict, dataclass
from typing import Any, Protocol
from urllib.parse import urlsplit

from .._typed_jobs import JobCheckpoint
from ..job_runtime import JobError, _scope, job_hash, job_value_hash
from ..workflows import NonRetryableError
from .files import FileBucket, FileOptions


def _parse_pipeline_trigger(value: Any) -> dict[str, Any]:
    if (
        not isinstance(value, dict)
        or set(value) != {"version", "kind", "identity"}
        or type(value["version"]) is not int
        or value["version"] != 1
        or value["kind"] not in ("event", "schedule", "tool")
        or not _hash(value["identity"])
    ):
        raise PipelineError("receipt_conflict")
    return copy.deepcopy(value)


class PipelineError(NonRetryableError):
    def __init__(self, code: str, step: str | None = None, job_run_id: str | None = None):
        super().__init__("pipeline:" + code + (":" + step if step else "") + (":" + job_run_id if job_run_id else ""))
        self.code, self.step, self.job_run_id = code, step, job_run_id


@dataclass(frozen=True, kw_only=True)
class PipelineFailure:
    code: str
    job_run_id: str | None = None


def pipeline_failure(value: Any) -> PipelineFailure | None:
    if not isinstance(value, str):
        return None
    parts = value.split(":")
    codes = {
        "invalid_input",
        "scope_mismatch",
        "receipt_conflict",
        "store_failed",
        "submission_unresolved",
        "job_failed",
        "invalid_result",
        "poll_limit",
        "operation_limit",
        "journal_limit",
        "artifact_changed",
        "request_failed",
        "acceptance_unknown",
        "deadline",
        "cancelled",
    }
    if len(parts) < 2 or parts[0] != "pipeline" or parts[1] not in codes:
        return None
    return PipelineFailure(code=parts[1], job_run_id=parts[-1] if _uuid(parts[-1]) else None)


def _uuid(v: Any) -> bool:
    return (
        isinstance(v, str)
        and re.fullmatch(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}", v) is not None
        and v != "00000000-0000-0000-0000-000000000000"
    )


def _hash(v: Any) -> bool:
    return isinstance(v, str) and re.fullmatch(r"[a-f0-9]{64}", v) is not None


def pipeline_name(v: Any) -> bool:
    return isinstance(v, str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}", v) is not None


@dataclass(frozen=True, kw_only=True)
class PipelineScope:
    base_url: str
    account_id: str
    app_id: str
    app_slug: str

    def __post_init__(self) -> None:
        u = urlsplit(self.base_url)
        if (
            u.scheme not in ("http", "https")
            or not u.netloc
            or u.username
            or u.password
            or u.query
            or u.fragment
            or self.base_url.rstrip("/") != self.base_url
            or not _uuid(self.account_id)
            or not _uuid(self.app_id)
            or re.fullmatch(r"[a-z0-9][a-z0-9-]{0,62}", self.app_slug) is None
        ):
            raise PipelineError("invalid_input")

    def to_dict(self) -> dict[str, str]:
        return asdict(self)

    @classmethod
    def from_dict(cls, value: Any) -> PipelineScope:
        try:
            if not isinstance(value, dict) or set(value) != {"base_url", "account_id", "app_id", "app_slug"}:
                raise ValueError
            return cls(**value)
        except (TypeError, ValueError, AttributeError):
            raise PipelineError("invalid_input") from None


def pipeline_job_identity(scope: PipelineScope, run_id: str, step: str) -> str:
    if not _uuid(run_id) or not pipeline_name(step):
        raise PipelineError("invalid_input")
    return job_value_hash([scope.to_dict(), run_id, step])


@dataclass(frozen=True, kw_only=True)
class PipelineJobReceipt:
    # All access returns a copy. No caller can mutate a durable intent through
    # the receipt returned by the SDK.
    _intent_json: str
    checkpoint: JobCheckpoint | None

    @property
    def intent(self) -> dict[str, Any]:
        return json.loads(self._intent_json)  # type: ignore[no-any-return]

    def to_dict(self) -> dict[str, Any]:
        return {"intent": self.intent, "checkpoint": self.checkpoint.to_dict() if self.checkpoint else None}

    @classmethod
    def from_dict(cls, value: Any) -> PipelineJobReceipt:
        try:
            if not isinstance(value, dict) or set(value) != {"intent", "checkpoint"}:
                raise ValueError
            i = value["intent"]
            if not isinstance(i, dict) or set(i) != {
                "version",
                "identity",
                "scope",
                "workflow_run_id",
                "step",
                "owner_id",
                "pipeline_sha256",
                "contract_sha256",
                "job_name",
                "values_sha256",
                "input_digest",
                "idempotency_key",
                "inputs",
            }:
                raise ValueError
            scope = PipelineScope.from_dict(i["scope"])
            if (
                type(i["version"]) is not int
                or i["version"] != 1
                or i["identity"] != pipeline_job_identity(scope, i["workflow_run_id"], i["step"])
                or i["idempotency_key"] != "pipeline-job:" + i["identity"]
                or not pipeline_name(i["owner_id"])
                or not all(_hash(i[k]) for k in ("pipeline_sha256", "contract_sha256", "values_sha256", "input_digest"))
                or not isinstance(i["job_name"], str)
                or not re.fullmatch(r"[a-z0-9][a-z0-9-]{1,38}[a-z0-9]", i["job_name"])
            ):
                raise ValueError
            inputs = i["inputs"]
            if (
                not isinstance(inputs, list)
                or not 1 <= len(inputs) <= 1000
                or any(
                    not isinstance(x, dict)
                    or set(x) != {"input_id", "binding_sha256", "source_task_index"}
                    or not isinstance(x["input_id"], str)
                    or not re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", x["input_id"])
                    or not _hash(x["binding_sha256"])
                    or x["source_task_index"] is not None
                    for x in inputs
                )
                or len({x["input_id"] for x in inputs}) != len(inputs)
            ):
                raise ValueError
            c = JobCheckpoint.from_dict(value["checkpoint"]) if value["checkpoint"] is not None else None
            if c is not None:
                match_pipeline_job(i, c)
            return cls(_intent_json=json.dumps(i, ensure_ascii=False, separators=(",", ":")), checkpoint=c)
        except (ValueError, TypeError, KeyError, AttributeError, PipelineError, JobError):
            raise PipelineError("receipt_conflict") from None


def match_pipeline_job(intent: dict[str, Any], checkpoint: JobCheckpoint) -> JobCheckpoint:
    c = checkpoint.to_dict()
    if (
        c["base_url"] != intent["scope"]["base_url"]
        or c["account_id"] != intent["scope"]["account_id"]
        or c["job_name"] != intent["job_name"]
        or c["contract_sha256"] != intent["contract_sha256"]
        or c["input_digest"] != intent["input_digest"]
        or c["source_run_id"] is not None
        or job_value_hash(c["inputs"]) != job_value_hash(intent["inputs"])
    ):
        raise PipelineError("receipt_conflict")
    return checkpoint


class PipelineJobStore(Protocol):
    async def load(self, identity: str) -> PipelineJobReceipt | None: ...
    async def reserve(self, intent: dict[str, Any]) -> tuple[bool, PipelineJobReceipt]: ...
    async def complete(self, intent: dict[str, Any], checkpoint: JobCheckpoint) -> PipelineJobReceipt: ...


PIPELINE_JOB_SCHEMA = """CREATE TABLE IF NOT EXISTS public.gregale_pipeline_jobs (
 identity text PRIMARY KEY CHECK (identity ~ '^[a-f0-9]{64}$'),
 receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
"""


class PostgresPipelineJobStore:
    """query(sql, args) executes one acknowledged autocommit statement.

    The adapter uses PostgreSQL $n placeholders, matching the Node contract.
    Never wrap this store in an ambient application transaction.
    """

    def __init__(self, query: Callable[[str, list[Any]], Awaitable[list[dict[str, Any]]]]):
        self._query = query

    async def load(self, identity: str) -> PipelineJobReceipt | None:
        rows = await self._query("SELECT receipt FROM public.gregale_pipeline_jobs WHERE identity=$1", [identity])
        if len(rows) > 1:
            raise PipelineError("receipt_conflict")
        return PipelineJobReceipt.from_dict(rows[0]["receipt"]) if rows else None

    async def reserve(self, intent: dict[str, Any]) -> tuple[bool, PipelineJobReceipt]:
        rows = await self._query(
            "INSERT INTO public.gregale_pipeline_jobs(identity,receipt) VALUES ($1,$2::jsonb) ON CONFLICT (identity) DO NOTHING RETURNING receipt",
            [intent["identity"], json.dumps({"intent": intent, "checkpoint": None})],
        )
        receipt = (
            PipelineJobReceipt.from_dict(rows[0]["receipt"]) if len(rows) == 1 else await self.load(intent["identity"])
        )
        old = receipt.intent if receipt is not None else {}
        matches = (
            job_value_hash(old) == job_value_hash(intent)
            if len(rows) == 1
            else job_value_hash(old | {"input_digest": None, "inputs": []})
            == job_value_hash(intent | {"input_digest": None, "inputs": []})
        )
        if len(rows) > 1 or receipt is None or not matches:
            raise PipelineError("receipt_conflict")
        return len(rows) == 1, receipt

    async def complete(self, intent: dict[str, Any], checkpoint: JobCheckpoint) -> PipelineJobReceipt:
        match_pipeline_job(intent, checkpoint)
        rows = await self._query(
            "UPDATE public.gregale_pipeline_jobs SET receipt=jsonb_set(receipt,'{checkpoint}',$3::jsonb) WHERE identity=$1 AND receipt->'intent'=$2::jsonb AND (receipt->'checkpoint'='null'::jsonb OR receipt->'checkpoint'=$3::jsonb) RETURNING receipt",
            [intent["identity"], json.dumps(intent), json.dumps(checkpoint.to_dict())],
        )
        if len(rows) != 1:
            raise PipelineError("receipt_conflict")
        return PipelineJobReceipt.from_dict(rows[0]["receipt"])


async def read_pipeline_artifact(
    files: FileBucket, artifact: dict[str, Any], owner_id: str, *, options: FileOptions
) -> bytes:
    a = copy.deepcopy(artifact)
    scope = {"base_url": files.base_url, "app_slug": files.app_slug, "bucket": files.bucket}
    if (
        type(a.get("version")) is not int
        or a["version"] != 1
        or not pipeline_name(owner_id)
        or a.get("owner_id") != owner_id
        or a.get("scope") != scope
        or not isinstance(a.get("key"), str)
        or not a["key"]
        or len(a["key"].encode()) > 1024
        or re.search(r"[\x00-\x1f]", a["key"])
        or type(a.get("size_bytes")) is not int
        or a["size_bytes"] < 0
        or not _hash(a.get("sha256"))
        or a.get("version_id") is not None
        and not _uuid(a["version_id"])
    ):
        raise PipelineError("artifact_changed")
    if a["size_bytes"] > options.max_bytes:
        raise PipelineError("journal_limit")

    async def read(check: Callable[[], None]) -> bytes:
        data = await files.download(
            a["key"],
            version_id=a.get("version_id"),
            options=FileOptions(timeout=options.timeout, max_bytes=a["size_bytes"] or 1),
        )
        check()
        if len(data) != a["size_bytes"] or job_hash(data) != a["sha256"]:
            raise PipelineError("artifact_changed")
        return data

    return await _scope(options.timeout, read)


def _parse_pipeline_lineage(value: Any) -> dict[str, Any]:
    if (
        not isinstance(value, dict)
        or set(value) != {"version", "parent_run_id", "batch_identity", "item_id"}
        or type(value["version"]) is not int
        or value["version"] != 1
        or not _uuid(value["parent_run_id"])
        or not _hash(value["batch_identity"])
        or not isinstance(value["item_id"], str)
        or re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", value["item_id"]) is None
    ):
        raise PipelineError("receipt_conflict")
    return copy.deepcopy(value)
