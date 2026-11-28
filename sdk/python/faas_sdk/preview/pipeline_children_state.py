"""Portable child receipts and an application-owned revisioned batch ledger."""

from __future__ import annotations

import json
import re
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import Any, Protocol, cast

from ..job_runtime import job_input_id, job_value_hash
from ..workflows import NonRetryableError
from .job_pipeline_client import PipelineCheckpoint
from .pipeline_state import PipelineScope, _hash, _uuid, pipeline_name


class PipelineChildrenError(NonRetryableError):
    def __init__(self, code: str):
        self.code = code
        super().__init__("pipeline-children:" + code)


def _json(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False)


def pipeline_children_identity(scope: PipelineScope, run_id: str, batch: str) -> str:
    if not _uuid(run_id) or not pipeline_name(batch):
        raise PipelineChildrenError("invalid_input")
    return job_value_hash(["pipeline-children", scope.to_dict(), run_id, batch])


def pipeline_children_intent(**value: Any) -> dict[str, Any]:
    try:
        scope = PipelineScope.from_dict(value["scope"])
        inputs = value["inputs"]
        if (
            set(value)
            != {
                "scope",
                "parent_run_id",
                "owner_id",
                "batch",
                "parent_sha256",
                "parent_input_sha256",
                "child_sha256",
                "child_workflow",
                "inputs",
                "concurrency",
                "timeout_ms",
                "failure_policy",
                "on_fail_fast",
                "on_parent_stopped",
                "on_deadline",
            }
            or not pipeline_name(value["owner_id"])
            or re.fullmatch(r"[a-z0-9][a-z0-9-]{1,38}[a-z0-9]", value["child_workflow"]) is None
            or not all(_hash(value[k]) for k in ("parent_sha256", "parent_input_sha256", "child_sha256"))
            or not isinstance(inputs, list)
            or not 1 <= len(inputs) <= 50
            or len({i["id"] for i in inputs}) != len(inputs)
            or any(
                not job_input_id(i["id"])
                or i["input_sha256"]
                != job_value_hash(
                    dict(
                        lineage=dict(
                            version=1,
                            parent_run_id=value["parent_run_id"],
                            batch_identity=pipeline_children_identity(scope, value["parent_run_id"], value["batch"]),
                            item_id=i["id"],
                        ),
                        version=1,
                        scope=scope.to_dict(),
                        owner_id=value["owner_id"],
                        contract_sha256=value["child_sha256"],
                        input=i["value"],
                    )
                )
                for i in inputs
            )
            or type(value["concurrency"]) is not int
            or not 1 <= value["concurrency"] <= 10
            or type(value["timeout_ms"]) is not int
            or not 1 <= value["timeout_ms"] <= 86400000
            or value["failure_policy"] not in ("collect", "fail_fast")
            or any(
                value[k] not in ("leave_running", "cancel_children")
                for k in ("on_fail_fast", "on_parent_stopped", "on_deadline")
            )
            or len(_json(value).encode()) > 65536
        ):
            raise ValueError
        return cast(
            dict[str, Any],
            json.loads(
                _json(
                    value
                    | {
                        "scope": scope.to_dict(),
                        "version": 1,
                        "identity": pipeline_children_identity(scope, value["parent_run_id"], value["batch"]),
                    }
                )
            ),
        )
    except Exception:
        raise PipelineChildrenError("invalid_input") from None


def match_pipeline_child(intent: dict[str, Any], index: int, value: Any) -> PipelineCheckpoint:
    try:
        c = value if isinstance(value, PipelineCheckpoint) else PipelineCheckpoint.from_dict(value)
        c = PipelineCheckpoint.from_dict(c.to_dict())
        if (
            c.run_id == intent["parent_run_id"]
            or c.scope.to_dict() != intent["scope"]
            or c.owner_id != intent["owner_id"]
            or c.contract_sha256 != intent["child_sha256"]
            or c.input_sha256 != intent["inputs"][index]["input_sha256"]
        ):
            raise ValueError
        return c
    except Exception:
        raise PipelineChildrenError("conflict") from None


@dataclass(frozen=True)
class PipelineChildrenReceipt:
    _json: str

    def to_dict(self) -> dict[str, Any]:
        return cast(dict[str, Any], json.loads(self._json))

    @property
    def intent(self) -> dict[str, Any]:
        return cast(dict[str, Any], self.to_dict()["intent"])

    @property
    def state(self) -> dict[str, Any]:
        return cast(dict[str, Any], self.to_dict()["state"])

    @property
    def revision(self) -> int:
        return cast(int, self.to_dict()["revision"])

    @property
    def expired(self) -> bool:
        return cast(bool, self.to_dict()["expired"])

    @classmethod
    def from_dict(cls, value: Any) -> PipelineChildrenReceipt:
        try:
            i, s = value["intent"], value["state"]
            base = {k: v for k, v in i.items() if k not in ("version", "identity")}
            if (
                set(value) != {"intent", "state", "revision", "expires_at", "expired"}
                or job_value_hash(pipeline_children_intent(**base)) != job_value_hash(i)
                or type(value["revision"]) is not int
                or not 0 <= value["revision"] <= 9007199254740991
                or type(value["expired"]) is not bool
                or set(s) != {"stop_reason", "parent_stopped", "children"}
                or s["stop_reason"] not in (None, "deadline", "child_failed", "parent_stopped")
                or type(s["parent_stopped"]) is not bool
                or not isinstance(s["children"], list)
                or len(s["children"]) != len(i["inputs"])
                or len(_json(s).encode()) > 131072
            ):
                raise ValueError
            datetime.fromisoformat(value["expires_at"].replace("Z", "+00:00"))
            ids = set()
            for n, c in enumerate(s["children"]):
                if (
                    c["phase"]
                    not in ("unstarted", "submission_unresolved", "running", "succeeded", "failed", "cancelled")
                    or type(c["cancel_requested"]) is not bool
                    or (c["phase"] in ("unstarted", "submission_unresolved")) != (c["checkpoint"] is None)
                    or (c["phase"] == "succeeded") != ("output" in c)
                    or c.get("native_status")
                    not in (None, "pending", "running", "awaiting_event", "succeeded", "failed", "dead")
                ):
                    raise ValueError
                if c["checkpoint"] is not None:
                    checkpoint = match_pipeline_child(i, n, c["checkpoint"])
                    if checkpoint.run_id in ids:
                        raise ValueError
                    ids.add(checkpoint.run_id)
            if sum(c["phase"] in ("running", "submission_unresolved") for c in s["children"]) > i["concurrency"]:
                raise ValueError
            return cls(_json(value))
        except Exception:
            raise PipelineChildrenError("conflict") from None


class PipelineChildrenStore(Protocol):
    async def open(self, intent: dict[str, Any]) -> PipelineChildrenReceipt: ...
    async def load(self, identity: str) -> PipelineChildrenReceipt | None: ...
    async def save(
        self, old: PipelineChildrenReceipt, state: dict[str, Any], admission: bool
    ) -> PipelineChildrenReceipt | None: ...
    async def scan(self, scope: PipelineScope, parent_sha256: str, limit: int) -> Sequence[PipelineChildrenReceipt]: ...


PIPELINE_CHILDREN_SCHEMA = """CREATE TABLE IF NOT EXISTS public.gregale_pipeline_children (
 identity text PRIMARY KEY CHECK (identity ~ '^[a-f0-9]{64}$'),
 intent jsonb NOT NULL CHECK (jsonb_typeof(intent)='object'),
 state jsonb NOT NULL CHECK (jsonb_typeof(state)='object'),
 revision bigint NOT NULL DEFAULT 0 CHECK (revision>=0),
 expires_at timestamptz NOT NULL,
 checked_at timestamptz NOT NULL DEFAULT '-infinity'
);
"""
_FIELDS = "jsonb_build_object('intent',intent,'state',state,'revision',revision,'expires_at',to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.MS\"Z\"'),'expired',expires_at<=clock_timestamp()) AS receipt"


class PostgresPipelineChildrenStore:
    """Explicit app migration; one acknowledged primary autocommit per query ($n parameters)."""

    def __init__(self, query: Callable[[str, list[Any]], Awaitable[list[dict[str, Any]]]]):
        self.query = query

    async def _rows(self, sql: str, values: list[Any]) -> list[PipelineChildrenReceipt]:
        return [PipelineChildrenReceipt.from_dict(r["receipt"]) for r in await self.query(sql, values)]

    async def load(self, identity: str) -> PipelineChildrenReceipt | None:
        rows = await self._rows(f"SELECT {_FIELDS} FROM public.gregale_pipeline_children WHERE identity=$1", [identity])
        if len(rows) > 1:
            raise PipelineChildrenError("conflict")
        return rows[0] if rows else None

    async def open(self, intent: dict[str, Any]) -> PipelineChildrenReceipt:
        state = dict(
            stop_reason=None,
            parent_stopped=False,
            children=[dict(phase="unstarted", checkpoint=None, cancel_requested=False) for _ in intent["inputs"]],
        )
        rows = await self._rows(
            f"INSERT INTO public.gregale_pipeline_children(identity,intent,state,expires_at) VALUES ($1,$2::jsonb,$3::jsonb,clock_timestamp()+($4::bigint*interval '1 millisecond')) ON CONFLICT (identity) DO NOTHING RETURNING {_FIELDS}",
            [intent["identity"], _json(intent), _json(state), intent["timeout_ms"]],
        )
        saved = rows[0] if rows else await self.load(intent["identity"])
        if saved is None or job_value_hash(saved.intent) != job_value_hash(intent):
            raise PipelineChildrenError("conflict")
        return saved

    async def save(
        self, old: PipelineChildrenReceipt, state: dict[str, Any], admission: bool
    ) -> PipelineChildrenReceipt | None:
        PipelineChildrenReceipt.from_dict(old.to_dict() | {"state": state, "revision": old.revision + 1})
        rows = await self._rows(
            f"UPDATE public.gregale_pipeline_children SET state=$4::jsonb,revision=revision+1 WHERE identity=$1 AND revision=$2 AND intent=$3::jsonb AND (NOT $5::boolean OR expires_at>clock_timestamp()) RETURNING {_FIELDS}",
            [old.intent["identity"], old.revision, _json(old.intent), _json(state), admission],
        )
        if len(rows) > 1:
            raise PipelineChildrenError("conflict")
        return rows[0] if rows else None

    async def scan(self, scope: PipelineScope, parent_sha256: str, limit: int) -> Sequence[PipelineChildrenReceipt]:
        if type(limit) is not int or not 1 <= limit <= 100:
            raise PipelineChildrenError("invalid_input")
        return await self._rows(
            f"UPDATE public.gregale_pipeline_children SET checked_at=clock_timestamp() WHERE identity IN (SELECT identity FROM public.gregale_pipeline_children WHERE intent->'scope'=$1::jsonb AND intent->>'parent_sha256'=$2 AND EXISTS (SELECT 1 FROM jsonb_array_elements(state->'children') c WHERE c->>'phase' IN ('submission_unresolved','running') OR c->>'phase'='unstarted' AND state->>'stop_reason' IS NULL) ORDER BY checked_at,identity LIMIT $3 FOR UPDATE SKIP LOCKED) RETURNING {_FIELDS}",
            [_json(scope.to_dict()), parent_sha256, limit],
        )
