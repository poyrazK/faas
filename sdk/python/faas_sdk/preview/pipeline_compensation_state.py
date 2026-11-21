"""Portable recovery contracts, immutable plans and application-owned receipts."""

from __future__ import annotations

import copy
import json
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any, Generic, Literal, Protocol, TypedDict, TypeVar

from ..job_runtime import JobContract, job_value_hash
from ._pipeline_compensation_protocol import SQL
from .event_pipeline_state import event_pipeline_scope
from .job_pipeline_client import PipelineCheckpoint
from .pipeline_state import PipelineScope, _hash, pipeline_name

I = TypeVar("I")
O = TypeVar("O")
CompensationAction = Literal["plan", "start", "inspect", "result", "cancel", "reconcile"]


class CompensationError(RuntimeError):
    def __init__(self, code: str):
        self.code = code
        super().__init__("Pipeline compensation: " + code)


@dataclass(frozen=True, kw_only=True)
class CompensationActor:
    actor_id: str
    owner_id: str


@dataclass(frozen=True, kw_only=True)
class CompensationCommand(Generic[I]):
    id: str
    effect_id: str
    description: str
    input: I

    def to_dict(self) -> dict[str, Any]:
        return dict(id=self.id, effect_id=self.effect_id, description=self.description, input=copy.deepcopy(self.input))


class CompensationTask(TypedDict):
    plan_id: str
    command_id: str
    recovery_run_id: str


class CompensationOutcome(TypedDict, Generic[O]):
    state: Literal["compensated", "already_compensated", "needs_review"]
    result: O


class CompensationEffect(TypedDict, Generic[O]):
    id: str
    outcome: CompensationOutcome[O] | None


class CompensationInput(TypedDict):
    plan_id: str
    binding_sha256: str


class CompensationResultItem(TypedDict, Generic[O]):
    id: str
    outcome: CompensationOutcome[O]


class CompensationResult(TypedDict, Generic[O]):
    state: Literal["completed", "needs_review", "rejected", "expired", "cancelled"]
    results: list[CompensationResultItem[O]]


@dataclass(frozen=True, kw_only=True)
class CompensationPlan(Generic[I]):
    _json: str

    def to_dict(self) -> dict[str, Any]:
        return dict(json.loads(self._json))

    @property
    def identity(self) -> str:
        return str(self.to_dict()["identity"])

    @property
    def owner_id(self) -> str:
        return str(self.to_dict()["owner_id"])

    @property
    def binding_sha256(self) -> str:
        return str(self.to_dict()["binding_sha256"])

    @property
    def source_sha256(self) -> str:
        return str(self.to_dict()["source_sha256"])

    @property
    def source(self) -> PipelineCheckpoint:
        return PipelineCheckpoint.from_dict(self.to_dict()["source"])

    @property
    def commands(self) -> tuple[CompensationCommand[I], ...]:
        return tuple(CompensationCommand(**c) for c in self.to_dict()["commands"])

    @property
    def input(self) -> CompensationInput:
        return CompensationInput(plan_id=self.identity, binding_sha256=self.binding_sha256)

    @classmethod
    def from_dict(cls, raw: Any, business: JobContract[I, Any]) -> CompensationPlan[I]:
        try:
            p = copy.deepcopy(raw)
            if (
                not isinstance(p, dict)
                or set(p)
                != {
                    "version",
                    "identity",
                    "scope",
                    "name",
                    "revision",
                    "config_sha256",
                    "owner_id",
                    "source",
                    "source_sha256",
                    "commands",
                    "binding_sha256",
                }
                or type(p["version"]) is not int
                or p["version"] != 1
                or not pipeline_name(p["name"])
                or not pipeline_name(p["revision"])
                or not pipeline_name(p["owner_id"])
                or not _hash(p["config_sha256"])
                or not _hash(p["source_sha256"])
            ):
                raise ValueError
            source = PipelineCheckpoint.from_dict(p["source"])
            scope = event_pipeline_scope(PipelineScope.from_dict(p["scope"]))
            if (
                source.owner_id != p["owner_id"]
                or source.scope != scope
                or p["identity"] != compensation_identity(scope, p["name"], source.run_id)
            ):
                raise ValueError
            base = {k: v for k, v in p.items() if k != "binding_sha256"}
            if (
                p["binding_sha256"] != job_value_hash(base)
                or not isinstance(p["commands"], list)
                or len(p["commands"]) > 20
            ):
                raise ValueError
            ids = set()
            effects = set()
            for c in p["commands"]:
                if (
                    set(c) != {"id", "effect_id", "description", "input"}
                    or not pipeline_name(c["id"])
                    or not pipeline_name(c["effect_id"])
                    or not isinstance(c["description"], str)
                    or not c["description"].strip()
                    or len(c["description"]) > 256
                    or c["id"] in ids
                    or c["effect_id"] in effects
                ):
                    raise ValueError
                business.input(c["input"])
                ids.add(c["id"])
                effects.add(c["effect_id"])
            encoded = json.dumps(p, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
            if len(encoded.encode()) > 65536:
                raise ValueError
            return cls(_json=encoded)
        except Exception:
            raise CompensationError("conflict") from None


def compensation_identity(scope: PipelineScope, name: str, run: str) -> str:
    return job_value_hash(["gregale.compensation.v1", event_pipeline_scope(scope).to_dict(), name, run])


def effect_identity(plan: CompensationPlan[Any], effect: str) -> str:
    p = plan.to_dict()
    return job_value_hash(["gregale.compensation-effect.v1", p["scope"], p["name"], p["source"]["run_id"], effect])


def compensation_contracts(business: JobContract[I, O], name: str) -> dict[str, JobContract[Any, Any]]:
    string = {"type": "string"}
    hash = {"type": "string", "minLength": 64, "maxLength": 64}
    uuid = {"type": "string", "minLength": 36, "maxLength": 36}

    def obj(properties: dict[str, Any]) -> dict[str, Any]:
        return dict(type="object", properties=properties, required=list(properties), additionalProperties=False)

    outcome = obj(
        dict(
            state=dict(type="string", enum=["compensated", "already_compensated", "needs_review"]),
            result=business.spec["output"],
        )
    )
    root_input = obj(dict(plan_id=hash, binding_sha256=hash))
    worker = JobContract[Any, Any](
        dict(
            version=1,
            name=business.spec["name"],
            input=obj(dict(plan_id=hash, command_id=string, recovery_run_id=uuid)),
            output=outcome,
        )
    )
    root = JobContract[Any, Any](
        dict(
            version=1,
            name="recover-" + name.replace("_", "-"),
            input=root_input,
            output=obj(
                dict(
                    state=dict(type="string", enum=["completed", "needs_review", "rejected", "expired", "cancelled"]),
                    results=dict(type="array", maxItems=20, items=obj(dict(id=string, outcome=outcome))),
                )
            ),
        )
    )
    review = JobContract[Any, Any](
        dict(
            version=1,
            name="review-" + name.replace("_", "-"),
            input=obj(
                dict(
                    plan_id=hash,
                    binding_sha256=hash,
                    source_run_id=uuid,
                    revision=string,
                    commands=dict(
                        type="array", maxItems=20, items=obj(dict(id=string, description=string, input_sha256=hash))
                    ),
                )
            ),
            output=obj(dict(reason=dict(type="string", maxLength=1000))),
        )
    )
    return dict(worker=worker, root=root, review=review)


class CompensationStore(Protocol):
    async def load(self, id: str) -> Any | None: ...
    async def reserve(self, plan: dict[str, Any]) -> Any: ...
    async def attempt(self, namespace: str, id: str) -> str | None: ...
    async def bind(self, namespace: str, id: str, plan_id: str) -> str: ...
    async def run(self, namespace: str, id: str) -> dict[str, str] | None: ...
    async def register_run(self, namespace: str, id: str, request_id: str, plan_id: str) -> None: ...
    async def effect(self, identity: str) -> dict[str, Any] | None: ...


class PostgresCompensationStore:
    def __init__(self, query: Callable[[str, list[Any]], Awaitable[list[dict[str, Any]]]]):
        self._query = query

    async def _read(self, sql: str, values: list[Any]) -> dict[str, Any] | None:
        rows = await self._query(sql, values)
        if len(rows) > 1:
            raise CompensationError("conflict")
        return rows[0] if rows else None

    async def load(self, id: str) -> Any | None:
        row = await self._read(SQL["load"], [id])
        return row["plan"] if row else None

    async def reserve(self, plan: dict[str, Any]) -> Any:
        row = await self._read(
            SQL["reserve"], [plan["identity"], json.dumps(plan, ensure_ascii=False, separators=(",", ":"))]
        )
        return row["plan"] if row else await self.load(plan["identity"])

    async def attempt(self, namespace: str, id: str) -> str | None:
        row = await self._read(SQL["attempt"], [namespace, id])
        return str(row["plan_id"]) if row else None

    async def bind(self, namespace: str, id: str, plan_id: str) -> str:
        row = await self._read(SQL["bind"], [namespace, id, plan_id])
        found = row["plan_id"] if row else await self.attempt(namespace, id)
        if found != plan_id:
            raise CompensationError("conflict")
        return plan_id

    async def run(self, namespace: str, id: str) -> dict[str, str] | None:
        row = await self._read(SQL["run"], [namespace, id])
        return dict(plan_id=str(row["plan_id"]), request_id=str(row["request_id"])) if row else None

    async def register_run(self, namespace: str, id: str, request_id: str, plan_id: str) -> None:
        row = await self._read(SQL["register_run"], [namespace, id, request_id, plan_id]) or await self.run(
            namespace, id
        )
        if not row or row["plan_id"] != plan_id or row["request_id"] != request_id:
            raise CompensationError("conflict")

    async def effect(self, identity: str) -> dict[str, Any] | None:
        return await self._read(SQL["effect"], [identity])
