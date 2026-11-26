"""Portable outbound action intent, approval contracts and observation receipts."""

from __future__ import annotations

import copy
import json
import re
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any, Generic, Literal, NotRequired, Protocol, TypedDict, TypeVar, cast

from ..job_runtime import JobContract, job_value_hash
from ._outbound_action_protocol import SQL
from .event_pipeline_state import event_pipeline_scope
from .pipeline_state import PipelineScope, _hash, _uuid, pipeline_name

I = TypeVar("I")
O = TypeVar("O")
ActionState = Literal["prepared", "unknown", "pending", "succeeded", "failed", "needs_review", "cancelled"]
ActionOperation = Literal["prepare", "inspect", "reconcile", "cancel", "start", "result"]


class OutboundActionError(Exception):
    def __init__(self, code: str):
        super().__init__("Outbound action: " + code)
        self.code = code


@dataclass(frozen=True, kw_only=True)
class OutboundActionActor:
    actor_id: str
    owner_id: str


class OutboundActionRequest(TypedDict):
    path: str
    content_type: Literal["application/json", "application/x-www-form-urlencoded"]
    body: str
    api_version: NotRequired[str]


class OutboundActionObservation(TypedDict, Generic[O]):
    state: ActionState
    code: str
    reference: NotRequired[str]
    result: NotRequired[O]


class OutboundActionReference(TypedDict):
    action_id: str
    binding_sha256: str


class OutboundActionView(OutboundActionReference, Generic[O]):
    outcome: OutboundActionObservation[O]


class OutboundActionReview(OutboundActionReference, Generic[I]):
    name: str
    revision: str
    input: I


class OutboundActionResult(TypedDict, Generic[O]):
    decision: Literal["approved", "rejected", "expired", "cancelled"]
    action: OutboundActionView[O]


class OutboundActionIntent(TypedDict, Generic[I]):
    version: Literal[1]
    identity: str
    scope: dict[str, Any]
    name: str
    revision: str
    config_sha256: str
    owner_id: str
    request_id: str
    input: I
    input_sha256: str
    request: OutboundActionRequest
    binding_sha256: str


class OutboundActionRecord(TypedDict, Generic[I, O]):
    intent: OutboundActionIntent[I]
    version: int
    outcome: OutboundActionObservation[O]


class OutboundActionStore(Protocol):
    async def load(self, identity: str) -> Any | None: ...
    async def reserve(self, intent: Any) -> Any: ...
    async def claim(self, identity: str, version: int) -> Any | None: ...
    async def observe(self, identity: str, version: int, outcome: Any) -> Any | None: ...
    async def cancel(self, identity: str, version: int) -> Any | None: ...
    async def run(self, identity: str, run_id: str) -> str | None: ...
    async def register_run(self, identity: str, run_id: str, digest: str) -> None: ...
    async def attempt(self, namespace: str, request_id: str) -> str | None: ...
    async def bind(self, namespace: str, request_id: str, identity: str) -> None: ...


def action_json(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False)


def outbound_action_identity(scope: PipelineScope, name: str, id: str) -> str:
    return job_value_hash(["gregale.outbound-action.v1", event_pipeline_scope(scope).to_dict(), name, id])


def action_reference(record: OutboundActionRecord[Any, Any]) -> OutboundActionReference:
    return {"action_id": record["intent"]["identity"], "binding_sha256": record["intent"]["binding_sha256"]}


def action_path(path: str) -> str:
    if (
        not isinstance(path, str)
        or len(path) > 2048
        or re.fullmatch(r"/[A-Za-z0-9_/?=&.%-]*", path) is None
        or "//" in path
        or "%" in path
        or any(x in (".", "..") for x in path.split("?")[0].split("/"))
        or len(path.split("?")) > 2
    ):
        raise OutboundActionError("invalid_input")
    return path


def action_request(raw: OutboundActionRequest) -> OutboundActionRequest:
    if (
        not isinstance(raw, dict)
        or set(raw) - {"path", "content_type", "body", "api_version"}
        or raw.get("content_type") not in ("application/json", "application/x-www-form-urlencoded")
        or not isinstance(raw.get("body"), str)
        or len(raw["body"].encode()) > 16384
        or "api_version" in raw
        and re.fullmatch(r"[A-Za-z0-9.-]{1,64}", raw["api_version"]) is None
    ):
        raise OutboundActionError("invalid_input")
    action_path(raw["path"])
    if "?" in raw["path"]:
        raise OutboundActionError("invalid_input")
    return copy.deepcopy(raw)


def action_observation(raw: OutboundActionObservation[O], business: JobContract[I, O]) -> OutboundActionObservation[O]:
    if (
        not isinstance(raw, dict)
        or set(raw) - {"state", "code", "reference", "result"}
        or raw.get("state")
        not in ("prepared", "unknown", "pending", "succeeded", "failed", "needs_review", "cancelled")
        or not pipeline_name(raw.get("code"))
        or "reference" in raw
        and re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", raw["reference"]) is None
        or raw["state"] in ("pending", "succeeded")
        and (not raw.get("reference") or "result" not in raw)
        or raw["state"] in ("prepared", "cancelled", "unknown")
        and ("reference" in raw or "result" in raw)
    ):
        raise OutboundActionError("conflict")
    value = copy.deepcopy(raw)
    if "result" in value:
        value["result"] = business.output(value["result"])
    if len(action_json(value).encode()) > 32768:
        raise OutboundActionError("limit")
    return value


def parse_outbound_action_record(raw: Any, business: JobContract[I, O]) -> OutboundActionRecord[I, O]:
    try:
        r = copy.deepcopy(raw)
        i = r["intent"]
        base = {k: v for k, v in i.items() if k != "binding_sha256"}
        if (
            set(r) != {"intent", "version", "outcome"}
            or set(i)
            != {
                "version",
                "identity",
                "scope",
                "name",
                "revision",
                "config_sha256",
                "owner_id",
                "request_id",
                "input",
                "input_sha256",
                "request",
                "binding_sha256",
            }
            or type(i["version"]) is not int
            or i["version"] != 1
            or not all(pipeline_name(i[k]) for k in ("name", "revision", "owner_id"))
            or not _uuid(i["request_id"])
            or not all(_hash(i[k]) for k in ("identity", "binding_sha256", "config_sha256", "input_sha256"))
            or type(r["version"]) is not int
            or not 0 <= r["version"] <= 2147483647
            or i["identity"]
            != outbound_action_identity(PipelineScope.from_dict(i["scope"]), i["name"], i["request_id"])
            or job_value_hash(base) != i["binding_sha256"]
            or job_value_hash(business.input(i["input"])) != i["input_sha256"]
        ):
            raise ValueError
        action_request(i["request"])
        r["outcome"] = action_observation(r["outcome"], business)
        if (r["outcome"]["state"] == "prepared") != (r["version"] == 0):
            raise ValueError
        return cast(OutboundActionRecord[I, O], r)
    except Exception:
        raise OutboundActionError("conflict") from None


def outbound_action_contracts(business: JobContract[I, O], name: str) -> dict[str, JobContract[Any, Any]]:
    string = {"type": "string"}
    hash_schema = dict(type="string", minLength=64, maxLength=64)
    reference: dict[str, Any] = dict(
        type="object",
        properties=dict(action_id=hash_schema, binding_sha256=hash_schema),
        required=["action_id", "binding_sha256"],
        additionalProperties=False,
    )
    outcome = dict(
        type="object",
        properties=dict(
            state=dict(
                type="string",
                enum=["prepared", "unknown", "pending", "succeeded", "failed", "needs_review", "cancelled"],
            ),
            code=string,
            reference=string,
            result=business.spec["output"],
        ),
        required=["state", "code"],
        additionalProperties=False,
    )
    view = dict(
        type="object",
        properties=reference["properties"] | dict(outcome=outcome),
        required=["action_id", "binding_sha256", "outcome"],
        additionalProperties=False,
    )
    return {
        "root": JobContract(
            dict(
                version=1,
                name="action-" + name.replace("_", "-"),
                input=reference,
                output=dict(
                    type="object",
                    properties=dict(
                        decision=dict(type="string", enum=["approved", "rejected", "expired", "cancelled"]), action=view
                    ),
                    required=["decision", "action"],
                    additionalProperties=False,
                ),
            )
        ),
        "review": JobContract(
            dict(
                version=1,
                name="review-" + name.replace("_", "-"),
                input=dict(
                    type="object",
                    properties=reference["properties"]
                    | dict(name=string, revision=string, input=business.spec["input"]),
                    required=["action_id", "binding_sha256", "name", "revision", "input"],
                    additionalProperties=False,
                ),
                output=dict(
                    type="object",
                    properties=dict(reason=dict(type="string", maxLength=1000)),
                    required=["reason"],
                    additionalProperties=False,
                ),
            )
        ),
    }


class PostgresOutboundActionStore:
    def __init__(self, query: Callable[[str, list[Any]], Awaitable[list[dict[str, Any]]]]):
        self._query = query

    async def _one(self, sql: str, args: list[Any]) -> dict[str, Any] | None:
        try:
            rows = await self._query(sql, args)
            if len(rows) > 1:
                raise ValueError
            return rows[0] if rows else None
        except Exception:
            raise OutboundActionError("store_failed") from None

    async def load(self, identity: str) -> Any | None:
        return await self._one(SQL["load"], [identity])

    async def reserve(self, intent: Any) -> Any:
        return await self._one(SQL["reserve"], [intent["identity"], action_json(intent)]) or await self.load(
            intent["identity"]
        )

    async def claim(self, identity: str, version: int) -> Any | None:
        return await self._one(SQL["claim"], [identity, version])

    async def observe(self, identity: str, version: int, outcome: Any) -> Any | None:
        return await self._one(SQL["observe"], [identity, version, action_json(outcome), outcome["state"]])

    async def cancel(self, identity: str, version: int) -> Any | None:
        return await self._one(SQL["cancel"], [identity, version])

    async def run(self, identity: str, run_id: str) -> str | None:
        row = await self._one(SQL["run"], [identity, run_id])
        return str(row["input_sha256"]) if row else None

    async def register_run(self, identity: str, run_id: str, digest: str) -> None:
        row = await self._one(SQL["register_run"], [identity, run_id, digest])
        found = row["input_sha256"] if row else await self.run(identity, run_id)
        if found != digest:
            raise OutboundActionError("conflict")

    async def attempt(self, namespace: str, request_id: str) -> str | None:
        row = await self._one(SQL["attempt"], [namespace, request_id])
        return str(row["action_id"]) if row else None

    async def bind(self, namespace: str, request_id: str, identity: str) -> None:
        row = await self._one(SQL["bind"], [namespace, request_id, identity])
        found = row["action_id"] if row else await self.attempt(namespace, request_id)
        if found != identity:
            raise OutboundActionError("conflict")
