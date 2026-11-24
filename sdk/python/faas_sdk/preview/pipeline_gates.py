"""Typed, durable application approval/input gates over native workflow events."""

from __future__ import annotations

import copy
import json
import re
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import Any, Generic, Literal, Protocol, TypeVar, cast

from ..job_runtime import JobContract, _scope, job_value_hash
from ..workflows import NonRetryableError, RetryPolicy, WorkflowContext
from .pipeline_state import PipelineScope, _hash, _uuid, pipeline_name

R = TypeVar("R")
D = TypeVar("D")
A = TypeVar("A")
T = TypeVar("T")
GateState = Literal["approved", "rejected", "submitted", "expired", "cancelled"]
GateKind = Literal["approval", "input"]


class PipelineGateError(NonRetryableError):
    def __init__(self, code: str):
        self.code = code
        super().__init__("pipeline-gate:" + code)


def _json(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False)


@dataclass(frozen=True, kw_only=True)
class PipelineGateOptions:
    timeout: float
    required_roles: tuple[str, ...]


@dataclass(frozen=True, kw_only=True)
class PipelineGateOutcome(Generic[D]):
    state: GateState
    actor_id: str | None
    payload: D | None
    decided_at: str

    def to_dict(self) -> dict[str, Any]:
        return copy.deepcopy(self.__dict__)


@dataclass(frozen=True, kw_only=True)
class PipelineGateActor:
    actor_id: str
    owner_id: str
    roles: tuple[str, ...]


def pipeline_gate_intent(**value: Any) -> dict[str, Any]:
    try:
        if (
            set(value)
            != {
                "kind",
                "scope",
                "workflow_run_id",
                "owner_id",
                "pipeline_sha256",
                "input_sha256",
                "name",
                "contract_sha256",
                "review",
                "required_roles",
                "timeout_ms",
            }
            or value["kind"] not in ("approval", "input")
            or not _uuid(value["workflow_run_id"])
            or not all(pipeline_name(value[k]) for k in ("owner_id", "name"))
            or not all(_hash(value[k]) for k in ("pipeline_sha256", "input_sha256", "contract_sha256"))
            or type(value["timeout_ms"]) is not int
            or not 1 <= value["timeout_ms"] <= 86400000
            or not isinstance(value["required_roles"], (list, tuple))
            or not 1 <= len(value["required_roles"]) <= 16
            or not all(pipeline_name(r) for r in value["required_roles"])
            or len(_json(value["review"]).encode()) > 16384
        ):
            raise ValueError
        scope = PipelineScope.from_dict(value["scope"]).to_dict()
        base = value | {
            "scope": scope,
            "required_roles": sorted(set(value["required_roles"])),
            "version": 1,
            "id": job_value_hash(["pipeline-gate", scope, value["workflow_run_id"], value["name"]]),
            "review_sha256": job_value_hash(value["review"]),
        }
        return copy.deepcopy(base | {"binding_sha256": job_value_hash(base)})
    except Exception:
        raise PipelineGateError("invalid_input") from None


@dataclass(frozen=True)
class PipelineGateRecord(Generic[R, D]):
    _json: str

    @property
    def intent(self) -> dict[str, Any]:
        return cast(dict[str, Any], json.loads(self._json)["intent"])

    @property
    def expires_at(self) -> str:
        return cast(str, json.loads(self._json)["expires_at"])

    @property
    def delivery(self) -> str:
        return cast(str, json.loads(self._json)["delivery"])

    @property
    def decision(self) -> PipelineGateOutcome[D] | None:
        value = json.loads(self._json)["decision"]
        return None if value is None else PipelineGateOutcome(**value)

    def to_dict(self) -> dict[str, Any]:
        return cast(dict[str, Any], json.loads(self._json))

    @classmethod
    def from_dict(cls, value: Any) -> PipelineGateRecord[R, D]:
        try:
            if (
                set(value) != {"intent", "expires_at", "decision", "delivery"}
                or re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z", value["expires_at"]) is None
            ):
                raise ValueError
            i = value["intent"]
            base = {k: v for k, v in i.items() if k not in ("version", "id", "review_sha256", "binding_sha256")}
            if job_value_hash(pipeline_gate_intent(**base)) != job_value_hash(i):
                raise ValueError
            datetime.fromisoformat(value["expires_at"].replace("Z", "+00:00"))
            if value["delivery"] not in ("pending", "delivered", "closed"):
                raise ValueError
            d = value["decision"]
            if d is None and value["delivery"] != "pending":
                raise ValueError
            if d is not None:
                if re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z", d["decided_at"]) is None:
                    raise ValueError
                datetime.fromisoformat(d["decided_at"].replace("Z", "+00:00"))
                if (
                    set(d) != {"state", "actor_id", "payload", "decided_at"}
                    or d["state"] not in ("approved", "rejected", "submitted", "expired", "cancelled")
                    or d["actor_id"] is not None
                    and not pipeline_name(d["actor_id"])
                    or d["state"] in ("approved", "rejected", "submitted")
                    and (
                        not d["actor_id"]
                        or (d["state"] == "submitted" if i["kind"] == "approval" else d["state"] != "submitted")
                    )
                    or d["state"] in ("expired", "cancelled")
                    and d["payload"] is not None
                    or d["state"] == "expired"
                    and d["actor_id"] is not None
                    or len(_json(d["payload"]).encode()) > 16384
                ):
                    raise ValueError
            return cls(
                _json({"intent": i, "expires_at": value["expires_at"], "decision": d, "delivery": value["delivery"]})
            )
        except Exception:
            raise PipelineGateError("conflict") from None


class PipelineGateStore(Protocol):
    async def open(self, intent: dict[str, Any]) -> PipelineGateRecord[Any, Any]: ...
    async def resolve(self, identity: str) -> PipelineGateRecord[Any, Any] | None: ...
    async def decide(self, identity: str, binding: str, proposal: dict[str, Any]) -> PipelineGateRecord[Any, Any]: ...
    async def mark(self, identity: str, decision: PipelineGateOutcome[Any], delivery: str) -> None: ...
    async def pending(
        self, limit: int, scope: PipelineScope, pipeline: str, contract: str
    ) -> Sequence[PipelineGateRecord[Any, Any]]: ...
    async def list(self, run_id: str, owner_id: str) -> Sequence[PipelineGateRecord[Any, Any]]: ...


PIPELINE_GATE_SCHEMA = """CREATE TABLE IF NOT EXISTS public.gregale_pipeline_gates (
 id text PRIMARY KEY CHECK (id ~ '^[a-f0-9]{64}$'),
 intent jsonb NOT NULL CHECK (jsonb_typeof(intent)='object'),
 expires_at timestamptz NOT NULL,
 checked_at timestamptz NOT NULL DEFAULT '-infinity',
 decision jsonb,
 delivery text NOT NULL DEFAULT 'pending' CHECK (delivery IN ('pending','delivered','closed'))
);
CREATE INDEX IF NOT EXISTS gregale_pipeline_gates_pending ON public.gregale_pipeline_gates(delivery,expires_at);
"""


def _date_sql(column: str) -> str:
    return f"to_char({column} AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.MS\"Z\"')"


_RETURNING = f"jsonb_build_object('intent',intent,'expires_at',{_date_sql('expires_at')},'decision',decision,'delivery',delivery) AS record"
_EXPIRY = f"jsonb_build_object('state','expired','actor_id',NULL,'payload',NULL,'decided_at',{_date_sql('clock_timestamp()')})"


class PostgresPipelineGateStore:
    """One primary autocommit query per call. Install schema explicitly.

    query uses PostgreSQL $n placeholders, matching PostgresPipelineJobStore.
    """

    def __init__(self, query: Callable[[str, list[Any]], Awaitable[list[dict[str, Any]]]]):
        self._query = query

    async def _rows(self, sql: str, values: list[Any]) -> list[PipelineGateRecord[Any, Any]]:
        return [PipelineGateRecord.from_dict(r["record"]) for r in await self._query(sql, values)]

    async def resolve(self, identity: str) -> PipelineGateRecord[Any, Any] | None:
        rows = await self._rows(
            f"UPDATE public.gregale_pipeline_gates SET decision=CASE WHEN decision IS NULL AND expires_at<=clock_timestamp() THEN {_EXPIRY} ELSE decision END WHERE id=$1 RETURNING {_RETURNING}",
            [identity],
        )
        if len(rows) > 1:
            raise PipelineGateError("conflict")
        return rows[0] if rows else None

    async def open(self, intent: dict[str, Any]) -> PipelineGateRecord[Any, Any]:
        rows = await self._rows(
            f"INSERT INTO public.gregale_pipeline_gates(id,intent,expires_at) VALUES ($1,$2::jsonb,clock_timestamp()+($3::bigint*interval '1 millisecond')) ON CONFLICT (id) DO NOTHING RETURNING {_RETURNING}",
            [intent["id"], _json(intent), intent["timeout_ms"]],
        )
        record = rows[0] if rows else await self.resolve(intent["id"])
        if record is None or job_value_hash(record.intent) != job_value_hash(intent):
            raise PipelineGateError("conflict")
        return record

    async def decide(self, identity: str, binding: str, proposal: dict[str, Any]) -> PipelineGateRecord[Any, Any]:
        rows = await self._rows(
            f"UPDATE public.gregale_pipeline_gates SET decision=CASE WHEN expires_at<=clock_timestamp() THEN {_EXPIRY} ELSE $3::jsonb || jsonb_build_object('decided_at',{_date_sql('clock_timestamp()')}) END WHERE id=$1 AND intent->>'binding_sha256'=$2 AND decision IS NULL RETURNING {_RETURNING}",
            [identity, binding, _json(proposal)],
        )
        record = rows[0] if rows else await self.resolve(identity)
        if record is None or record.intent["binding_sha256"] != binding:
            raise PipelineGateError("conflict")
        return record

    async def mark(self, identity: str, decision: PipelineGateOutcome[Any], delivery: str) -> None:
        rows = await self._rows(
            f"UPDATE public.gregale_pipeline_gates SET delivery=$3 WHERE id=$1 AND decision=$2::jsonb RETURNING {_RETURNING}",
            [identity, _json(decision.to_dict()), delivery],
        )
        if len(rows) != 1:
            raise PipelineGateError("conflict")

    async def pending(
        self, limit: int, scope: PipelineScope, pipeline: str, contract: str
    ) -> Sequence[PipelineGateRecord[Any, Any]]:
        if type(limit) is not int or not 1 <= limit <= 100:
            raise PipelineGateError("invalid_input")
        return await self._rows(
            f"UPDATE public.gregale_pipeline_gates SET checked_at=clock_timestamp() WHERE id IN (SELECT id FROM public.gregale_pipeline_gates WHERE delivery='pending' AND intent->'scope'=$2::jsonb AND intent->>'pipeline_sha256'=$3 AND intent->>'contract_sha256'=$4 ORDER BY (decision IS NULL AND expires_at>clock_timestamp()),checked_at,expires_at,id LIMIT $1 FOR UPDATE SKIP LOCKED) RETURNING {_RETURNING}",
            [limit, _json(scope.to_dict()), pipeline, contract],
        )

    async def list(self, run_id: str, owner_id: str) -> Sequence[PipelineGateRecord[Any, Any]]:
        return await self._rows(
            f"SELECT {_RETURNING} FROM public.gregale_pipeline_gates WHERE intent->>'workflow_run_id'=$1 AND intent->>'owner_id'=$2 ORDER BY id LIMIT 100",
            [run_id, owner_id],
        )


class PipelineGateTransport(Protocol):
    async def active(self, intent: dict[str, Any]) -> bool: ...
    async def publish(self, intent: dict[str, Any]) -> None: ...


class NativePipelineGateTransport:
    """Backend-only isolated clients. Per-operation clients are always closed."""

    def __init__(
        self,
        *,
        scope: PipelineScope,
        token: str,
        contract: JobContract[Any, Any],
        timeout: float = 10,
        **httpx_args: Any,
    ):
        self.scope, self.token, self.contract, self.timeout = scope, token, contract, timeout
        self.httpx_args = httpx_args

    def _client(self, intent: dict[str, Any]) -> Any:
        from .job_pipeline_client import JobPipelineClient

        if intent["scope"] != self.scope.to_dict() or intent["pipeline_sha256"] != self.contract.sha256:
            raise PipelineGateError("forbidden")
        return JobPipelineClient(
            base_url=self.scope.base_url,
            token=self.token,
            scope=self.scope,
            owner_id=intent["owner_id"],
            contract=self.contract,
            **self.httpx_args,
        )

    async def active(self, intent: dict[str, Any]) -> bool:
        from .job_pipeline_client import PipelineCheckpoint

        async with self._client(intent) as client:
            checkpoint = PipelineCheckpoint(
                version=1,
                run_id=intent["workflow_run_id"],
                scope=self.scope,
                owner_id=intent["owner_id"],
                contract_sha256=intent["pipeline_sha256"],
                input_sha256=intent["input_sha256"],
            )
            state = await client.resume(checkpoint).inspect(timeout=self.timeout)
            return bool(not state.cancelled and state.status in ("pending", "running", "awaiting_event"))

    async def publish(self, intent: dict[str, Any]) -> None:
        async with self._client(intent) as client:
            event = "pipeline-gate." + intent["id"]

            async def work(request: Any) -> None:
                ack = await request(
                    "/v1/workflows/runs/" + intent["workflow_run_id"] + "/events",
                    "POST",
                    {
                        "event_name": event,
                        "payload": {"version": 1, "gate_id": intent["id"], "binding_sha256": intent["binding_sha256"]},
                    },
                    "pipeline-gate:" + intent["id"],
                )
                if ack.get("status") != "received" or ack.get("event_name") != event:
                    raise PipelineGateError("request_failed")

            await client._operation(self.timeout, work)


@dataclass(frozen=True, kw_only=True)
class PipelineGateView(Generic[R, D]):
    can_decide: bool
    id: str
    run_id: str
    name: str
    kind: GateKind
    review: R
    binding_sha256: str
    expires_at: str
    decision: PipelineGateOutcome[D] | None
    delivery: str


class PipelineGateService(Generic[R, D, A]):
    def __init__(
        self,
        *,
        scope: PipelineScope,
        pipeline_sha256: str,
        contract: JobContract[R, D],
        store: PipelineGateStore,
        transport: PipelineGateTransport,
        authorize: Callable[[PipelineGateRecord[Any, Any], str, A], Awaitable[PipelineGateActor]],
    ):
        if not _hash(pipeline_sha256):
            raise PipelineGateError("invalid_input")
        self.pipeline_sha256 = pipeline_sha256
        self.scope, self.contract, self.store, self.transport, self.authorize = (
            scope,
            contract,
            store,
            transport,
            authorize,
        )

    def _owned(self, raw: PipelineGateRecord[Any, Any]) -> PipelineGateRecord[R, D]:
        record: PipelineGateRecord[R, D] = PipelineGateRecord.from_dict(raw.to_dict())
        i, d = record.intent, record.decision
        if (
            i["scope"] != self.scope.to_dict()
            or i["contract_sha256"] != self.contract.sha256
            or i["pipeline_sha256"] != self.pipeline_sha256
        ):
            raise PipelineGateError("forbidden")
        self.contract.input(i["review"])
        if d is not None and d.state in ("approved", "rejected", "submitted"):
            self.contract.output(d.payload)
        return record

    async def _access(
        self, identity: str, action: str, auth: A, check: Callable[[], None]
    ) -> tuple[PipelineGateRecord[R, D], PipelineGateActor, bool]:
        if not _hash(identity):
            raise PipelineGateError("forbidden")
        raw = await self.store.resolve(identity)
        check()
        if raw is None:
            raise PipelineGateError("forbidden")
        record = self._owned(raw)
        actor = await self.authorize(record, action, auth)
        check()
        if (
            not pipeline_name(actor.actor_id)
            or actor.owner_id != record.intent["owner_id"]
            or action == "decide"
            and not all(r in actor.roles for r in record.intent["required_roles"])
        ):
            raise PipelineGateError("forbidden")
        active = await self.transport.active(record.intent)
        check()
        if not active and action == "decide" and record.decision is None:
            raise PipelineGateError("inactive")
        return record, actor, active

    def _view(self, r: PipelineGateRecord[R, D], actor: PipelineGateActor | None = None) -> PipelineGateView[R, D]:
        i = r.intent
        return PipelineGateView(
            can_decide=r.decision is None
            and actor is not None
            and all(role in actor.roles for role in r.intent["required_roles"]),
            id=i["id"],
            run_id=i["workflow_run_id"],
            name=i["name"],
            kind=i["kind"],
            review=self.contract.input(i["review"]),
            binding_sha256=i["binding_sha256"],
            expires_at=r.expires_at,
            decision=r.decision,
            delivery=r.delivery,
        )

    async def _deliver(self, r: PipelineGateRecord[R, D], check: Callable[[], None]) -> None:
        if r.decision is None or r.delivery != "pending":
            return
        active = await self.transport.active(r.intent)
        check()
        if active:
            await self.transport.publish(r.intent)
            check()
        await self.store.mark(r.intent["id"], r.decision, "delivered" if active else "closed")
        check()

    async def _execute(self, timeout: float, fn: Callable[[Callable[[], None]], Awaitable[T]]) -> T:
        async def safe(check: Callable[[], None]) -> T:
            try:
                result = await fn(check)
                check()
                return result
            except PipelineGateError:
                raise
            except Exception:
                check()
                raise PipelineGateError("request_failed") from None

        return await _scope(timeout, safe)

    async def inspect(self, identity: str, auth: A, *, timeout: float) -> PipelineGateView[R, D]:
        async def work(check: Callable[[], None]) -> PipelineGateView[R, D]:
            r, actor, active = await self._access(identity, "inspect", auth, check)
            return self._view(r, actor if active else None)

        return await self._execute(timeout, work)

    async def decide(
        self,
        identity: str,
        *,
        binding_sha256: str,
        state: Literal["approved", "rejected", "submitted", "cancelled"],
        auth: A,
        payload: D | None = None,
        timeout: float,
    ) -> PipelineGateView[R, D]:
        async def work(check: Callable[[], None]) -> PipelineGateView[R, D]:
            r, actor, _ = await self._access(identity, "decide", auth, check)
            if (
                binding_sha256 != r.intent["binding_sha256"]
                or state not in ("approved", "rejected", "submitted", "cancelled")
                or state != "cancelled"
                and (state == "submitted" if r.intent["kind"] == "approval" else state != "submitted")
            ):
                raise PipelineGateError("conflict")
            proposal = {
                "state": state,
                "actor_id": actor.actor_id,
                "payload": None if state == "cancelled" else self.contract.output(payload),
            }
            if len(_json(proposal["payload"]).encode()) > 16384:
                raise PipelineGateError("invalid_input")
            saved = self._owned(await self.store.decide(identity, binding_sha256, proposal))
            check()
            d = saved.decision
            if (
                d is None
                or d.state != state
                or d.actor_id != actor.actor_id
                or job_value_hash(d.payload) != job_value_hash(proposal["payload"])
            ):
                raise PipelineGateError("conflict")
            try:
                await self._deliver(saved, check)
            except Exception:
                check()  # durable pending delivery is recovered by drain()
            raw = await self.store.resolve(identity)
            check()
            if raw is None:
                raise PipelineGateError("conflict")
            return self._view(self._owned(raw))

        return await self._execute(timeout, work)

    async def drain(self, limit: int, *, timeout: float) -> int:
        async def work(check: Callable[[], None]) -> int:
            count = 0
            for raw in await self.store.pending(limit, self.scope, self.pipeline_sha256, self.contract.sha256):
                check()
                r = await self.store.resolve(raw.intent["id"])
                check()
                if r is None:
                    raise PipelineGateError("conflict")
                if r.decision is None and not await self.transport.active(r.intent):
                    check()
                    r = await self.store.decide(
                        r.intent["id"],
                        r.intent["binding_sha256"],
                        {"state": "cancelled", "actor_id": None, "payload": None},
                    )
                if r.decision is not None:
                    await self._deliver(self._owned(r), check)
                    count += 1
            return count

        return await self._execute(timeout, work)


async def run_pipeline_gate(
    wf: WorkflowContext,
    store: PipelineGateStore,
    intent: dict[str, Any],
    contract: JobContract[R, D],
    timeout: float,
    claim: Callable[[], None],
    budget: Callable[[Any], None],
) -> PipelineGateOutcome[D]:
    def verify(raw: PipelineGateRecord[Any, Any]) -> PipelineGateRecord[R, D]:
        r: PipelineGateRecord[R, D] = PipelineGateRecord.from_dict(raw.to_dict())
        if job_value_hash(r.intent) != job_value_hash(intent):
            raise PipelineGateError("conflict")
        return r

    async def io(work: Callable[[Callable[[], None]], Awaitable[T]]) -> T:
        async def safe(check: Callable[[], None]) -> T:
            try:
                result = await work(check)
                check()
                return result
            except PipelineGateError:
                raise
            except Exception:
                check()
                raise RuntimeError("pipeline-gate:request_failed") from None

        return await _scope(timeout, safe)

    async def open_gate(_: Any) -> dict[str, Any]:
        async def work(check: Callable[[], None]) -> dict[str, Any]:
            r = verify(await store.open(intent))
            check()
            return {"id": intent["id"], "binding_sha256": intent["binding_sha256"], "expires_at": r.expires_at}

        return await io(work)

    claim()
    opened = await wf.step(
        "pipeline-gate/" + intent["name"] + "/open", open_gate, retry=RetryPolicy(max_attempts=3, backoff="exponential")
    )
    budget(opened)
    if opened["id"] != intent["id"] or opened["binding_sha256"] != intent["binding_sha256"]:
        raise PipelineGateError("conflict")
    for _wake in range(16):
        claim()
        await wf.wait_for_event(
            "pipeline-gate/" + intent["name"] + "/wait",
            event="pipeline-gate." + intent["id"],
            timeout=intent["timeout_ms"] / 1000,
        )

        async def resolve(_: Any) -> Any:
            async def work(check: Callable[[], None]) -> Any:
                raw = await store.resolve(intent["id"])
                check()
                if raw is None:
                    raise PipelineGateError("conflict")
                d = verify(raw).decision
                return None if d is None else d.to_dict()

            return await io(work)

        claim()
        decision = await wf.step(
            "pipeline-gate/" + intent["name"] + "/resolve",
            resolve,
            retry=RetryPolicy(max_attempts=3, backoff="exponential"),
        )
        budget(decision)
        if decision is not None:
            raw: PipelineGateRecord[R, D] = PipelineGateRecord.from_dict(
                {"intent": intent, "expires_at": opened["expires_at"], "decision": decision, "delivery": "pending"}
            )
            outcome = verify(raw).decision
            assert outcome is not None
            if outcome.state in ("approved", "rejected", "submitted"):
                contract.output(outcome.payload)
            return outcome
    raise PipelineGateError("wake_limit")
