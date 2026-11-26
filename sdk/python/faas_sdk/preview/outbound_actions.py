"""Typed external writes with exact human approval and read-only reconciliation."""

from __future__ import annotations

import asyncio
import copy
import os
import re
from collections.abc import Awaitable, Callable, Mapping
from typing import Any, Generic, Protocol, TypeVar, cast
from urllib.parse import urlsplit

import httpx

from ..job_runtime import JobContract, JobError, _scope, job_value_hash
from ..workflows import RetryPolicy, WorkflowContext, WorkflowDefinition
from .event_pipeline_state import event_pipeline_scope
from .job_pipeline_client import JobPipelineClient, PipelineCheckpoint
from .job_pipelines import JobPipeline, JobPipelineContext, JobPipelineOptions
from .outbound_action_state import (
    ActionOperation,
    OutboundActionActor,
    OutboundActionError,
    OutboundActionIntent,
    OutboundActionObservation,
    OutboundActionRecord,
    OutboundActionReference,
    OutboundActionRequest,
    OutboundActionResult,
    OutboundActionReview,
    OutboundActionStore,
    OutboundActionView,
    action_json,
    action_observation,
    action_path,
    action_reference,
    action_request,
    outbound_action_contracts,
    outbound_action_identity,
    parse_outbound_action_record,
)
from .pipeline_gates import PipelineGateOptions, PipelineGateRecord, PipelineGateStore
from .pipeline_state import PipelineJobStore, PipelineScope, _hash, _uuid, pipeline_name
from .pipeline_tool_state import PipelineToolActor, PipelineToolStore
from .pipeline_tools import PipelineTool, PipelineToolRequest

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")
T = TypeVar("T")


class OutboundActionResponse:
    def __init__(self, status: int, body: Any):
        self.status, self.body = status, body


class OutboundActionReader(Protocol):
    async def get(self, path: str) -> OutboundActionResponse: ...


class OutboundActionProvider(Protocol[I, O]):
    name: str
    configuration: Mapping[str, str | int | bool]

    def request(self, input: I, identity: str) -> OutboundActionRequest: ...
    async def verify(self, reader: OutboundActionReader) -> bool: ...
    def decode(
        self, response: OutboundActionResponse, intent: OutboundActionIntent[I]
    ) -> OutboundActionObservation[O]: ...
    async def reconcile(
        self, reader: OutboundActionReader, record: OutboundActionRecord[I, O]
    ) -> OutboundActionObservation[O]: ...


class _Transport:
    def __init__(self, base: str, args: dict[str, Any], check: Callable[[], None], limit: int, version: str | None):
        self.base, self.args, self.check, self.limit, self.version, self.reads = base, args, check, limit, version, 0

    async def get(self, path: str) -> OutboundActionResponse:
        self.reads += 1
        if self.reads > self.limit:
            raise OutboundActionError("limit")
        return await self.send(path)

    async def send(
        self, path: str, request: OutboundActionRequest | None = None, key: str | None = None
    ) -> OutboundActionResponse:
        self.check()
        action_path(path)
        headers = {"Accept": "application/json"}
        if self.version:
            headers["Stripe-Version"] = self.version
        if request:
            headers.update({"Content-Type": request["content_type"], "Idempotency-Key": str(key)})
        async with httpx.AsyncClient(trust_env=False, follow_redirects=False, timeout=None, **self.args) as client:
            async with client.stream(
                "POST" if request else "GET",
                self.base + path,
                headers=headers,
                content=request["body"].encode() if request else None,
            ) as response:
                data = bytearray()
                async for chunk in response.aiter_bytes():
                    self.check()
                    data.extend(chunk)
                    if len(data) > 1048576:
                        raise OutboundActionError("limit")
                self.check()
                import json

                return OutboundActionResponse(response.status_code, json.loads(data.decode("utf-8")))


class OutboundAction(Generic[I, O, A]):
    def __init__(
        self,
        business: JobContract[I, O],
        *,
        name: str,
        revision: str,
        scope: PipelineScope,
        integration_id: str,
        token: str,
        store: OutboundActionStore,
        provider: OutboundActionProvider[I, O],
        tool_store: PipelineToolStore,
        gate_store: PipelineGateStore,
        review_base_url: str,
        required_roles: tuple[str, ...],
        authorize: Callable[[str, I, A, ActionOperation], Awaitable[OutboundActionActor | None]],
        authorize_execution: Callable[[OutboundActionIntent[I], str], Awaitable[bool]],
        relay_url: str | None = None,
        approval_timeout: float = 3600,
        request_timeout: float = 10,
        max_reads: int = 10,
        httpx_args: dict[str, Any] | None = None,
    ):
        self.business_contract = JobContract[I, O](business.spec)
        self._scope, self._name, self._revision = event_pipeline_scope(scope), name, revision
        roles = sorted(set(required_roles))
        relay = relay_url or os.environ.get("GREGALE_OUTBOUND_URL", "")
        parsed = urlsplit(relay)
        if (
            parsed.scheme not in ("http", "https")
            or not parsed.netloc
            or parsed.username
            or parsed.password
            or parsed.query
            or parsed.fragment
            or re.fullmatch(r"[a-z][a-z0-9_]{0,29}", name) is None
            or not pipeline_name(revision)
            or not pipeline_name(provider.name)
            or not _uuid(integration_id)
            or not 1 <= len(roles) <= 16
            or not all(pipeline_name(r) for r in roles)
            or not 0 < approval_timeout <= 86400
            or int(approval_timeout * 1000) != approval_timeout * 1000
            or not 0 < request_timeout <= 2147483.647
            or type(max_reads) is not int
            or not 1 <= max_reads <= 20
        ):
            raise OutboundActionError("invalid_input")
        action_path(parsed.path or "/")
        self._httpx_args = dict(httpx_args or {})
        if set(self._httpx_args) & {
            "base_url",
            "headers",
            "auth",
            "params",
            "cookies",
            "follow_redirects",
            "timeout",
            "trust_env",
        }:
            raise OutboundActionError("invalid_input")
        self._base = relay.rstrip("/") + "/i/" + integration_id
        self._provider, self._store, self._gates = provider, store, gate_store
        self._authorize, self._authorize_execution = authorize, authorize_execution
        self._token, self._roles, self._approval_timeout, self._timeout, self._max_reads = (
            token,
            roles,
            approval_timeout,
            request_timeout,
            max_reads,
        )
        contracts = outbound_action_contracts(self.business_contract, name)
        self.contract: JobContract[OutboundActionReference, OutboundActionResult[O]] = contracts["root"]
        self.review_contract: JobContract[OutboundActionReview[I], dict[str, str]] = contracts["review"]
        self._config = job_value_hash(
            [
                "gregale.outbound-action-config.v1",
                self._scope.to_dict(),
                name,
                revision,
                integration_id,
                provider.name,
                copy.deepcopy(dict(provider.configuration)),
                business.sha256,
                roles,
                int(approval_timeout * 1000),
                max_reads,
            ]
        )
        self._namespace = job_value_hash(["gregale.outbound-tool.v1", self._scope.to_dict(), name])
        self.tool: PipelineTool[OutboundActionReference, OutboundActionResult[O], A] = PipelineTool(
            self.contract,
            name="action_" + name,
            description="Review and execute a typed external action.",
            scope=self._scope,
            store=tool_store,
            gate_store=gate_store,
            review_base_url=review_base_url,
            token=token,
            httpx_args=self._httpx_args,
            authorize=self._tool_authorize,
        )
        self.definition = WorkflowDefinition(name=self.contract.spec["name"], run=self._run)

    async def _op(self, timeout: float, work: Callable[[Callable[[], None], Callable[[], float]], Awaitable[T]]) -> T:
        async def run(check: Callable[[], None]) -> T:
            end = asyncio.get_running_loop().time() + timeout

            def left() -> float:
                check()
                return max(0.000001, end - asyncio.get_running_loop().time())

            return await work(check, left)

        try:
            return await _scope(timeout, run)
        except OutboundActionError:
            raise
        except JobError as e:
            raise OutboundActionError("deadline" if e.code == "deadline" else "provider_failed") from None
        except Exception:
            raise OutboundActionError("provider_failed") from None

    async def _stored(self, fn: Callable[[], Awaitable[T]], check: Callable[[], None]) -> T:
        check()
        try:
            value = await fn()
            check()
            return value
        except OutboundActionError:
            raise
        except Exception:
            check()
            raise OutboundActionError("store_failed") from None

    def _bound(self, raw: Any) -> OutboundActionRecord[I, O]:
        r = parse_outbound_action_record(raw, self.business_contract)
        i = r["intent"]
        if (
            i["name"] != self._name
            or i["revision"] != self._revision
            or i["config_sha256"] != self._config
            or job_value_hash(i["scope"]) != job_value_hash(self._scope.to_dict())
            or job_value_hash(i["request"])
            != job_value_hash(action_request(self._provider.request(copy.deepcopy(i["input"]), i["identity"])))
        ):
            raise OutboundActionError("conflict")
        return r

    async def _load(self, id: str, check: Callable[[], None]) -> OutboundActionRecord[I, O]:
        if not _hash(id):
            raise OutboundActionError("invalid_input")
        raw = await self._stored(lambda: self._store.load(id), check)
        if raw is None:
            raise OutboundActionError("not_found")
        r = self._bound(raw)
        if r["intent"]["identity"] != id:
            raise OutboundActionError("conflict")
        return r

    def _view(self, r: OutboundActionRecord[I, O]) -> OutboundActionView[O]:
        return {**action_reference(r), "outcome": copy.deepcopy(r["outcome"])}

    def _review(self, r: OutboundActionRecord[I, O]) -> OutboundActionReview[I]:
        return {
            **action_reference(r),
            "name": self._name,
            "revision": self._revision,
            "input": copy.deepcopy(r["intent"]["input"]),
        }

    async def _actor(
        self, id: str, input: I, access: A, action: ActionOperation, check: Callable[[], None]
    ) -> OutboundActionActor:
        try:
            actor = await self._authorize(id, copy.deepcopy(input), access, action)
        except Exception:
            check()
            raise OutboundActionError("unauthorized") from None
        check()
        if actor is None or not pipeline_name(actor.actor_id) or not pipeline_name(actor.owner_id):
            raise OutboundActionError("unauthorized")
        return actor

    async def _owned(
        self, id: str, access: A, action: ActionOperation, check: Callable[[], None]
    ) -> OutboundActionRecord[I, O]:
        r = await self._load(id, check)
        actor = await self._actor(r["intent"]["request_id"], r["intent"]["input"], access, action, check)
        if actor.owner_id != r["intent"]["owner_id"]:
            raise OutboundActionError("unauthorized")
        return r

    async def _native(
        self, r: OutboundActionRecord[I, O], run_id: str, check: Callable[[], None], left: Callable[[], float]
    ) -> PipelineCheckpoint:
        try:
            allowed = await self._authorize_execution(copy.deepcopy(r["intent"]), run_id)
        except Exception:
            allowed = False
        check()
        if allowed is not True:
            raise OutboundActionError("unauthorized")
        async with JobPipelineClient(
            base_url=self._scope.base_url,
            scope=self._scope,
            owner_id=r["intent"]["owner_id"],
            contract=self.contract,
            token=self._token,
            httpx_args=self._httpx_args,
        ) as client:
            run = await client.resume_by_id(run_id, timeout=left())
            info = await run.inspect(timeout=left())
            check()
            if info.cancelled or info.status not in ("pending", "running", "awaiting_event"):
                raise OutboundActionError("execution_inactive")
            return run.checkpoint

    async def _proof(
        self, r: OutboundActionRecord[I, O], run_id: str, check: Callable[[], None], left: Callable[[], float]
    ) -> None:
        checkpoint = await self._native(r, run_id, check, left)
        if (
            await self._stored(lambda: self._store.run(r["intent"]["identity"], run_id), check)
            != checkpoint.input_sha256
        ):
            raise OutboundActionError("conflict")
        all_gates = await self._stored(lambda: self._gates.list(run_id, r["intent"]["owner_id"]), check)
        if len(all_gates) > 100:
            raise OutboundActionError("limit")
        gates = [g for g in all_gates if g.intent["name"] == "review"]
        if len(gates) != 1:
            raise OutboundActionError("approval_required")
        raw = await self._stored(lambda: self._gates.resolve(gates[0].intent["id"]), check)
        if raw is None:
            raise OutboundActionError("approval_required")
        g: PipelineGateRecord[Any, Any] = PipelineGateRecord.from_dict(raw.to_dict())
        i = g.intent
        if (
            i["kind"] != "approval"
            or i["workflow_run_id"] != run_id
            or i["owner_id"] != r["intent"]["owner_id"]
            or job_value_hash(i["scope"]) != job_value_hash(self._scope.to_dict())
            or i["pipeline_sha256"] != self.contract.sha256
            or i["input_sha256"] != checkpoint.input_sha256
            or i["contract_sha256"] != self.review_contract.sha256
            or i["timeout_ms"] != int(self._approval_timeout * 1000)
            or i["required_roles"] != self._roles
            or job_value_hash(i["review"]) != job_value_hash(self._review(r))
            or g.decision is None
            or g.decision.state != "approved"
        ):
            raise OutboundActionError("approval_required")
        self.review_contract.output(g.decision.payload)

    def _transport(self, r: OutboundActionRecord[I, O], check: Callable[[], None]) -> _Transport:
        return _Transport(
            self._base, self._httpx_args, check, self._max_reads, r["intent"]["request"].get("api_version")
        )

    async def _save(
        self, r: OutboundActionRecord[I, O], observation: OutboundActionObservation[O], check: Callable[[], None]
    ) -> OutboundActionRecord[I, O]:
        o = action_observation(observation, self.business_contract)
        if o["state"] in ("prepared", "cancelled"):
            raise OutboundActionError("conflict")
        if r["outcome"].get("reference") and o.get("reference") and r["outcome"]["reference"] != o["reference"]:
            o = {"state": "needs_review", "code": "provider_reference_conflict", "reference": r["outcome"]["reference"]}
        if r["outcome"].get("reference") and not o.get("reference") and o["state"] in ("needs_review", "failed"):
            o["reference"] = r["outcome"]["reference"]
        if (
            o["state"] == "unknown"
            and r["outcome"]["state"] == "pending"
            or job_value_hash(o) == job_value_hash(r["outcome"])
        ):
            return r
        raw = await self._stored(lambda: self._store.observe(r["intent"]["identity"], r["version"], o), check)
        return await self._load(r["intent"]["identity"], check) if raw is None else self._bound(raw)

    async def _tool_authorize(
        self, request: PipelineToolRequest[OutboundActionReference], access: A, action: Any
    ) -> PipelineToolActor:
        def check() -> None:
            return None  # PipelineTool owns and fences the outer total deadline.

        if request.input is not None:
            r = await self._owned(request.input["action_id"], access, action, check)
            if request.input["binding_sha256"] != r["intent"]["binding_sha256"]:
                raise OutboundActionError("conflict")
            await self._store.bind(self._namespace, request.request_id, r["intent"]["identity"])
        else:
            identity = await self._store.attempt(self._namespace, request.request_id)
            if identity is None:
                raise OutboundActionError("not_found")
            r = await self._owned(identity, access, action, check)
        return PipelineToolActor(subject_id=self._name, owner_id=r["intent"]["owner_id"])

    async def _run(self, envelope: Any, wf: WorkflowContext) -> Any:
        ref = self.contract.input(envelope["input"])

        async def prepare(_ctx: Any) -> Any:
            async def work(check: Callable[[], None], left: Callable[[], float]) -> Any:
                r = await self._load(ref["action_id"], check)
                if (
                    ref["binding_sha256"] != r["intent"]["binding_sha256"]
                    or envelope["owner_id"] != r["intent"]["owner_id"]
                ):
                    raise OutboundActionError("conflict")
                checkpoint = await self._native(r, wf.run_id, check, left)
                if checkpoint.input_sha256 != job_value_hash(envelope):
                    raise OutboundActionError("conflict")
                await self._stored(
                    lambda: self._store.register_run(r["intent"]["identity"], wf.run_id, checkpoint.input_sha256), check
                )
                return r

            return await self._op(self._timeout, work)

        r = self._bound(await wf.step("action/prepare", prepare))

        async def run(_ref: OutboundActionReference, context: JobPipelineContext) -> OutboundActionResult[O]:
            decision = await context.approval(
                "review",
                self.review_contract,
                self._review(r),
                options=PipelineGateOptions(timeout=self._approval_timeout, required_roles=tuple(self._roles)),
            )

            async def dispatch(_ctx: Any) -> OutboundActionView[O]:
                return await self.execute(ref["action_id"], wf.run_id, timeout=self._timeout)

            action = (
                await wf.step("action/dispatch", dispatch, retry=RetryPolicy(max_attempts=1))
                if decision.state == "approved"
                else self._view(r)
            )
            return {"decision": cast(Any, decision.state), "action": action}

        pipeline = JobPipeline(
            self.contract,
            scope=self._scope,
            jobs={},
            store=cast(PipelineJobStore, object()),
            gate_store=self._gates,
            options=JobPipelineOptions(request_timeout=self._timeout, max_journal_bytes=262144),
            run=run,
        )
        return await pipeline.definition.run(envelope, wf)

    async def prepare(self, input: I, request_id: str, access: A, *, timeout: float) -> OutboundActionView[O]:
        captured = self.business_contract.input(input)

        async def work(check: Callable[[], None], _left: Callable[[], float]) -> OutboundActionView[O]:
            if not _uuid(request_id):
                raise OutboundActionError("invalid_input")
            if len(action_json(captured).encode()) > 8192:
                raise OutboundActionError("limit")
            a = await self._actor(request_id, captured, access, "prepare", check)
            identity = outbound_action_identity(self._scope, self._name, request_id)
            base = dict(
                version=1,
                identity=identity,
                scope=self._scope.to_dict(),
                name=self._name,
                revision=self._revision,
                config_sha256=self._config,
                owner_id=a.owner_id,
                request_id=request_id,
                input=captured,
                input_sha256=job_value_hash(captured),
                request=action_request(self._provider.request(copy.deepcopy(captured), identity)),
            )
            intent = base | dict(binding_sha256=job_value_hash(base))
            r = self._bound(await self._stored(lambda: self._store.reserve(intent), check))
            if job_value_hash(r["intent"]) != job_value_hash(intent):
                raise OutboundActionError("conflict")
            return self._view(r)

        return await self._op(timeout, work)

    async def inspect(self, identity: str, access: A, *, timeout: float) -> OutboundActionView[O]:
        async def work(check: Callable[[], None], _left: Callable[[], float]) -> OutboundActionView[O]:
            return self._view(await self._owned(identity, access, "inspect", check))

        return await self._op(timeout, work)

    async def cancel(self, identity: str, access: A, *, timeout: float) -> OutboundActionView[O]:
        async def work(check: Callable[[], None], _left: Callable[[], float]) -> OutboundActionView[O]:
            r = await self._owned(identity, access, "cancel", check)
            if r["outcome"]["state"] != "prepared":
                return self._view(r)
            raw = await self._stored(lambda: self._store.cancel(identity, r["version"]), check)
            return self._view(await self._load(identity, check) if raw is None else self._bound(raw))

        return await self._op(timeout, work)

    async def reconcile(self, identity: str, access: A, *, timeout: float) -> OutboundActionView[O]:
        async def work(check: Callable[[], None], _left: Callable[[], float]) -> OutboundActionView[O]:
            r = await self._owned(identity, access, "reconcile", check)
            if r["outcome"]["state"] not in ("unknown", "pending"):
                return self._view(r)
            transport = self._transport(r, check)
            if not await self._provider.verify(transport):
                raise OutboundActionError("conflict")
            check()
            observation = await self._provider.reconcile(transport, copy.deepcopy(r))
            check()
            await self._owned(identity, access, "reconcile", check)
            return self._view(await self._save(r, observation, check))

        return await self._op(timeout, work)

    async def execute(self, identity: str, run_id: str, *, timeout: float) -> OutboundActionView[O]:
        dispatched: list[OutboundActionRecord[I, O]] = []

        async def work(check: Callable[[], None], left: Callable[[], float]) -> OutboundActionView[O]:
            r = await self._load(identity, check)
            await self._proof(r, run_id, check, left)
            if r["outcome"]["state"] != "prepared":
                return self._view(r)
            transport = self._transport(r, check)
            if not await self._provider.verify(transport):
                raise OutboundActionError("conflict")
            await self._proof(r, run_id, check, left)
            raw = await self._stored(lambda: self._store.claim(identity, r["version"]), check)
            if raw is None:
                return self._view(await self._load(identity, check))
            r = self._bound(raw)
            if r["outcome"]["state"] != "unknown" or r["version"] != 1:
                raise OutboundActionError("conflict")
            dispatched.append(r)
            await self._proof(r, run_id, check, left)
            response = await transport.send(
                r["intent"]["request"]["path"], r["intent"]["request"], "gregale-action:" + identity
            )
            check()
            return self._view(await self._save(r, self._provider.decode(response, copy.deepcopy(r["intent"])), check))

        try:
            return await self._op(timeout, work)
        except (asyncio.CancelledError, Exception):
            if dispatched:
                return self._view(dispatched[0])
            raise
