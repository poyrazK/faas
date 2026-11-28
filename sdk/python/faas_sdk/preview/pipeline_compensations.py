"""Reviewed recovery workflows; SQL business effects and outcome receipts commit atomically."""

from __future__ import annotations

import asyncio
import copy
import json
import re
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any, Generic, TypeVar, cast

from .._typed_jobs import JobRunOptions, TypedJob
from ..job_runtime import JobContext, JobContract, JobError, JobInput, _scope, job_value_hash
from ..workflows import RetryPolicy, WorkflowContext, WorkflowDefinition
from ._pipeline_compensation_protocol import SQL
from .event_outbox_state import EventOutboxError
from .event_outbox_transaction import EventOutboxPool, EventOutboxTransaction, with_event_outbox_transaction
from .event_pipeline_state import event_pipeline_scope
from .job_pipeline_client import JobPipelineClient, PipelineCheckpoint
from .job_pipelines import JobPipeline, JobPipelineContext, JobPipelineOptions, PipelineJob
from .pipeline_compensation_state import (
    CompensationAction,
    CompensationActor,
    CompensationCommand,
    CompensationEffect,
    CompensationError,
    CompensationInput,
    CompensationOutcome,
    CompensationPlan,
    CompensationResult,
    CompensationResultItem,
    CompensationStore,
    CompensationTask,
    compensation_contracts,
    compensation_identity,
    effect_identity,
)
from .pipeline_gates import PipelineGateError, PipelineGateOptions, PipelineGateRecord, PipelineGateStore
from .pipeline_state import PipelineJobReceipt, PipelineJobStore, PipelineScope, pipeline_job_identity, pipeline_name
from .pipeline_tool_state import PipelineToolReceipt, PipelineToolStore
from .pipeline_tools import PipelineTool, PipelineToolRequest

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")
T = TypeVar("T")


@dataclass(frozen=True, kw_only=True)
class CompensationSourceStage:
    definition: PipelineJob[Any, Any]
    read_only: bool


@dataclass(frozen=True, kw_only=True)
class CompensationSource:
    checkpoint: PipelineCheckpoint
    stages: tuple[dict[str, Any], ...]

    def to_dict(self) -> dict[str, Any]:
        return dict(checkpoint=self.checkpoint.to_dict(), stages=copy.deepcopy(list(self.stages)))


@dataclass(frozen=True, kw_only=True)
class CompensationContext:
    effect_id: str
    owner_id: str
    _check: Callable[[], None]

    def check_cancelled(self) -> None:
        self._check()


class PipelineCompensation(Generic[I, O, A]):
    def __init__(
        self,
        business: JobContract[I, O],
        *,
        name: str,
        revision: str,
        scope: PipelineScope,
        source_contract: JobContract[Any, Any],
        source_stages: dict[str, CompensationSourceStage],
        source_store: PipelineJobStore,
        job: PipelineJob[CompensationTask, CompensationOutcome[O]],
        job_store: PipelineJobStore,
        store: CompensationStore,
        tool_store: PipelineToolStore,
        gate_store: PipelineGateStore,
        token: str,
        review_base_url: str,
        authorize: Callable[[str, A, CompensationAction], Awaitable[CompensationActor | None]],
        plan: Callable[[CompensationSource, A], Awaitable[list[CompensationCommand[I]]]],
        authorize_execution: Callable[[CompensationPlan[I], str], Awaitable[bool]],
        required_roles: tuple[str, ...],
        approval_timeout: float = 3600,
        request_timeout: float = 10,
        poll_delay: float = 1,
        httpx_args: dict[str, Any] | None = None,
    ):
        self._business = JobContract[I, O](business.spec)
        self._scope = event_pipeline_scope(scope)
        self._source_contract = JobContract[Any, Any](source_contract.spec)
        self._stages = sorted(source_stages.items())
        roles = sorted(set(required_roles))
        if (
            re.fullmatch(r"[a-z][a-z0-9_]{0,29}", name) is None
            or not pipeline_name(revision)
            or not 1 <= len(self._stages) <= 10
            or any(not pipeline_name(n) or type(s.read_only) is not bool for n, s in self._stages)
            or not 1 <= len(roles) <= 16
            or any(not pipeline_name(r) for r in roles)
            or not 0 < request_timeout <= 2147483.647
            or not 0 < approval_timeout <= 86400
            or int(approval_timeout * 1000) != approval_timeout * 1000
        ):
            raise CompensationError("invalid_input")
        self._revision = revision
        self._name, self._source_store, self._job, self._job_store, self._store, self._tool_store, self._gates = (
            name,
            source_store,
            job,
            job_store,
            store,
            tool_store,
            gate_store,
        )
        self._token, self._authorize, self._planner, self._authorize_execution = (
            token,
            authorize,
            plan,
            authorize_execution,
        )
        self._roles, self._timeout, self._approval_timeout, self._delay = (
            tuple(roles),
            request_timeout,
            approval_timeout,
            poll_delay,
        )
        self._httpx_args = dict(httpx_args or {})
        if set(self._httpx_args) & {
            "base_url",
            "token",
            "headers",
            "auth",
            "params",
            "cookies",
            "follow_redirects",
            "timeout",
        }:
            raise CompensationError("invalid_input")
        contracts = compensation_contracts(self._business, name)
        self.worker_contract: JobContract[CompensationTask, CompensationOutcome[O]] = contracts["worker"]
        self.contract: JobContract[CompensationInput, CompensationResult[O]] = contracts["root"]
        self.review_contract = contracts["review"]
        if self.worker_contract.sha256 != job.contract.sha256:
            raise CompensationError("conflict")
        self._config = job_value_hash(
            [
                "gregale.compensation-config.v1",
                self._scope.to_dict(),
                name,
                revision,
                self._business.sha256,
                self._source_contract.sha256,
                [[n, s.read_only, s.definition.contract.sha256] for n, s in self._stages],
                roles,
                int(approval_timeout * 1000),
            ]
        )
        self._namespace = job_value_hash([self._scope.to_dict(), name])
        self.tool = PipelineTool[CompensationInput, CompensationResult[O], A](
            self.contract,
            name="recover_" + name,
            description="Review and compensate verified effects of a failed pipeline.",
            scope=self._scope,
            store=tool_store,
            token=token,
            gate_store=gate_store,
            review_base_url=review_base_url,
            authorize=self._tool_authorize,
            httpx_args=self._httpx_args,
        )
        self.definition = WorkflowDefinition(name=self.contract.spec["name"], run=self._run)

    async def _execute(
        self, timeout: float, work: Callable[[Callable[[], None], Callable[[], float]], Awaitable[T]]
    ) -> T:
        async def run(check: Callable[[], None]) -> T:
            end = asyncio.get_running_loop().time() + timeout

            def left() -> float:
                check()
                return max(0.000001, end - asyncio.get_running_loop().time())

            value = await work(check, left)
            check()
            return value

        try:
            return await _scope(timeout, run)
        except CompensationError:
            raise
        except JobError as e:
            raise CompensationError("deadline" if e.code == "deadline" else "request_failed") from None
        except Exception:
            raise CompensationError("request_failed") from None

    async def _stored(self, fn: Callable[[], Awaitable[T]], check: Callable[[], None]) -> T:
        check()
        try:
            value = await fn()
            check()
            return value
        except CompensationError:
            raise
        except Exception:
            check()
            raise CompensationError("store_failed") from None

    def _bound(self, raw: Any) -> CompensationPlan[I]:
        p = CompensationPlan.from_dict(raw, self._business)
        data = p.to_dict()
        if (
            data["name"] != self._name
            or data["revision"] != self._revision
            or data["config_sha256"] != self._config
            or p.source.contract_sha256 != self._source_contract.sha256
            or job_value_hash(data["scope"]) != job_value_hash(self._scope.to_dict())
        ):
            raise CompensationError("conflict")
        return p

    async def _actor(
        self, run_id: str, access: A, action: CompensationAction, check: Callable[[], None]
    ) -> CompensationActor:
        try:
            a = copy.deepcopy(await self._authorize(run_id, access, action))
            check()
            if a is None or not pipeline_name(a.actor_id) or not pipeline_name(a.owner_id):
                raise ValueError
            return a
        except Exception:
            check()
            raise CompensationError("unauthorized") from None

    async def _owned(
        self, id: str, access: A, action: CompensationAction, check: Callable[[], None]
    ) -> CompensationPlan[I]:
        p = self._bound(await self._stored(lambda: self._store.load(id), check))
        a = await self._actor(p.source.run_id, access, action, check)
        if a.owner_id != p.owner_id:
            raise CompensationError("unauthorized")
        return p

    def _client(self, owner: str, source: bool = False) -> JobPipelineClient[Any, Any]:
        return JobPipelineClient(
            base_url=self._scope.base_url,
            scope=self._scope,
            owner_id=owner,
            contract=self._source_contract if source else self.contract,
            token=self._token,
            httpx_args=self._httpx_args,
        )

    async def _source(
        self, id: str, owner: str, full: bool, check: Callable[[], None], left: Callable[[], float]
    ) -> CompensationSource:
        async with self._client(owner, True) as client:
            run = await client.resume_by_id(id, timeout=left())
            info = await run.inspect(timeout=left())
            check()
            if info.status not in ("failed", "dead"):
                raise CompensationError("source_active")
            checkpoint = run.checkpoint
        evidence = []
        for step, s in self._stages:
            raw = await self._stored(
                lambda: self._source_store.load(pipeline_job_identity(self._scope, id, step)), check
            )
            if raw is None:
                continue
            r = PipelineJobReceipt.from_dict(raw.to_dict())
            i = r.intent
            if (
                i["workflow_run_id"] != id
                or i["owner_id"] != owner
                or i["pipeline_sha256"] != self._source_contract.sha256
                or i["contract_sha256"] != s.definition.contract.sha256
                or i["job_name"] != s.definition.contract.spec["name"]
                or job_value_hash(i["scope"]) != job_value_hash(self._scope.to_dict())
            ):
                raise CompensationError("conflict")
            if r.checkpoint is None:
                raise CompensationError("source_unresolved")
            if len(r.checkpoint.inputs) > 50:
                raise CompensationError("limit")
            handle = s.definition.job.resume(r.checkpoint)
            progress = await handle.status(options=JobRunOptions(timeout=left()))
            check()
            if progress.status in ("queued", "running"):
                raise CompensationError("source_active")
            if not s.read_only and progress.status != "succeeded":
                raise CompensationError("source_effects_unknown")
            results = []
            if full and progress.status == "succeeded":
                items = await handle.results(options=JobRunOptions(timeout=left(), max_items=50))
                check()
                if len(items) != progress.total or any(x.outcome != "succeeded" for x in items):
                    raise CompensationError("source_effects_unknown")
                results = [
                    dict(input_id=x.input_id, index=x.index, attempt=x.attempt, outcome=x.outcome, result=x.result)
                    for x in items
                ]
            evidence.append(
                dict(step=step, read_only=s.read_only, receipt=r.to_dict(), status=progress.status, results=results)
            )
            if len(json.dumps(evidence, ensure_ascii=False, separators=(",", ":")).encode()) > 262144:
                raise CompensationError("limit")
        return CompensationSource(checkpoint=checkpoint, stages=tuple(evidence))

    def _review(self, p: CompensationPlan[I]) -> dict[str, Any]:
        return dict(
            plan_id=p.identity,
            binding_sha256=p.binding_sha256,
            source_run_id=p.source.run_id,
            revision=p.to_dict()["revision"],
            commands=[
                dict(id=c.id, description=c.description, input_sha256=job_value_hash(c.input)) for c in p.commands
            ],
        )

    async def _load_execution(
        self, p: CompensationPlan[I], id: str, check: Callable[[], None], left: Callable[[], float]
    ) -> None:
        async with self._client(p.owner_id) as client:
            run = await client.resume_by_id(id, timeout=left())
            info = await run.inspect(timeout=left())
            check()
            checkpoint = run.checkpoint
        attempt = await self._stored(lambda: self._store.run(self._namespace, id), check)
        if attempt is None or attempt["plan_id"] != p.identity:
            raise CompensationError("conflict")
        expected = job_value_hash(
            dict(
                version=1,
                scope=self._scope.to_dict(),
                owner_id=p.owner_id,
                contract_sha256=self.contract.sha256,
                input=p.input,
                trigger=dict(
                    version=1,
                    kind="tool",
                    identity=job_value_hash(
                        [
                            "gregale.pipeline-tool.v1",
                            self._scope.to_dict(),
                            "recover_" + self._name,
                            self._name,
                            attempt["request_id"],
                        ]
                    ),
                ),
            )
        )
        if checkpoint.input_sha256 != expected:
            raise CompensationError("conflict")
        if info.cancelled or info.status not in ("pending", "running", "awaiting_event"):
            raise CompensationError("execution_inactive")
        rows = await self._stored(lambda: self._gates.list(id, p.owner_id), check)
        gates = [PipelineGateRecord[Any, Any].from_dict(g.to_dict()) for g in rows if g.intent["name"] == "review"]
        if len(gates) != 1:
            raise CompensationError("approval_required")
        raw = await self._stored(lambda: self._gates.resolve(gates[0].intent["id"]), check)
        if raw is None:
            raise CompensationError("approval_required")
        g = PipelineGateRecord[Any, Any].from_dict(raw.to_dict())
        i = g.intent
        if (
            i["kind"] != "approval"
            or i["timeout_ms"] != int(self._approval_timeout * 1000)
            or i["required_roles"] != list(self._roles)
            or i["workflow_run_id"] != id
            or i["owner_id"] != p.owner_id
            or i["pipeline_sha256"] != self.contract.sha256
            or i["contract_sha256"] != self.review_contract.sha256
            or i["input_sha256"] != checkpoint.input_sha256
            or job_value_hash(i["scope"]) != job_value_hash(self._scope.to_dict())
            or job_value_hash(i["review"]) != job_value_hash(self._review(p))
            or g.decision is None
            or g.decision.state != "approved"
        ):
            raise CompensationError("approval_required")
        self.review_contract.output(g.decision.payload)
        await self._source(p.source.run_id, p.owner_id, False, check, left)
        if not await self._authorize_execution(p, id):
            raise CompensationError("unauthorized")
        check()

    async def _tool_authorize(self, request: PipelineToolRequest[CompensationInput], access: A, action: Any) -> Any:
        async def work(check: Callable[[], None], left: Callable[[], float]) -> Any:
            if action == "start":
                if request.input is None:
                    raise CompensationError("invalid_input")
                p = await self._owned(request.input["plan_id"], access, action, check)
                if request.input["binding_sha256"] != p.binding_sha256:
                    raise CompensationError("conflict")
                await self._source(p.source.run_id, p.owner_id, False, check, left)
                await self._stored(lambda: self._store.bind(self._namespace, request.request_id, p.identity), check)
            else:
                id = await self._stored(lambda: self._store.attempt(self._namespace, request.request_id), check)
                if id is None:
                    raise CompensationError("conflict")
                p = await self._owned(id, access, action, check)
            from .pipeline_tool_state import PipelineToolActor

            return PipelineToolActor(subject_id=self._name, owner_id=p.owner_id)

        return await self._execute(self._timeout, work)

    async def plan(self, source_run_id: str, access: A, *, timeout: float) -> CompensationPlan[I]:
        async def work(check: Callable[[], None], left: Callable[[], float]) -> CompensationPlan[I]:
            a = await self._actor(source_run_id, access, "plan", check)
            evidence = await self._source(source_run_id, a.owner_id, True, check, left)
            id = compensation_identity(self._scope, self._name, source_run_id)
            source_sha = job_value_hash(evidence.to_dict())
            old = await self._stored(lambda: self._store.load(id), check)
            if old is not None:
                p = self._bound(old)
                if p.owner_id != a.owner_id or p.source_sha256 != source_sha:
                    raise CompensationError("conflict")
                return p
            commands = copy.deepcopy(await self._planner(copy.deepcopy(evidence), access))
            check()
            base = dict(
                version=1,
                identity=id,
                scope=self._scope.to_dict(),
                name=self._name,
                revision=self._revision,
                config_sha256=self._config,
                owner_id=a.owner_id,
                source=evidence.checkpoint.to_dict(),
                source_sha256=source_sha,
                commands=[c.to_dict() for c in commands],
            )
            p = CompensationPlan.from_dict(base | dict(binding_sha256=job_value_hash(base)), self._business)
            self.review_contract.input(self._review(p))
            if len(json.dumps(self._review(p), ensure_ascii=False, separators=(",", ":")).encode()) > 16384:
                raise CompensationError("limit")
            saved = self._bound(await self._stored(lambda: self._store.reserve(p.to_dict()), check))
            if saved.to_dict() != p.to_dict():
                raise CompensationError("conflict")
            return saved

        return await self._execute(timeout, work)

    async def inspect_plan(self, id: str, access: A, *, timeout: float) -> CompensationPlan[I]:
        return await self._execute(timeout, lambda check, left: self._owned(id, access, "inspect", check))

    async def effects(self, id: str, access: A, *, timeout: float) -> list[CompensationEffect[O]]:
        async def work(check: Callable[[], None], left: Callable[[], float]) -> list[CompensationEffect[O]]:
            p = await self._owned(id, access, "inspect", check)
            out: list[CompensationEffect[O]] = []
            for c in p.commands:
                raw = await self._stored(lambda: self._store.effect(effect_identity(p, c.effect_id)), check)
                if raw and raw["command_sha256"] != job_value_hash([self._config, c.to_dict()]):
                    raise CompensationError("conflict")
                out.append(dict(id=c.id, outcome=self.worker_contract.output(raw["outcome"]) if raw else None))
            return out

        return await self._execute(timeout, work)

    async def _run(self, envelope: dict[str, Any], wf: WorkflowContext) -> Any:
        ref = self.contract.input(envelope["input"])

        async def prepare(_ctx: Any) -> dict[str, Any]:
            async def work(check: Callable[[], None], left: Callable[[], float]) -> dict[str, Any]:
                p = self._bound(await self._stored(lambda: self._store.load(ref["plan_id"]), check))
                if (
                    ref["binding_sha256"] != p.binding_sha256
                    or envelope["owner_id"] != p.owner_id
                    or envelope["contract_sha256"] != self.contract.sha256
                    or job_value_hash(envelope["scope"]) != job_value_hash(self._scope.to_dict())
                ):
                    raise CompensationError("conflict")
                if not await self._authorize_execution(p, wf.run_id):
                    raise CompensationError("unauthorized")
                raw = await self._tool_store.load(envelope.get("trigger", {}).get("identity"))
                if raw is None:
                    raise CompensationError("conflict")
                receipt = PipelineToolReceipt.from_dict(raw.to_dict())
                i = receipt.intent
                if (
                    i["name"] != "recover_" + self._name
                    or i["subject_id"] != self._name
                    or i["owner_id"] != p.owner_id
                    or i["contract_sha256"] != self.contract.sha256
                    or i["input_sha256"] != job_value_hash(envelope)
                    or job_value_hash(i["scope"]) != job_value_hash(self._scope.to_dict())
                    or receipt.checkpoint is not None
                    and receipt.checkpoint.run_id != wf.run_id
                    or await self._store.attempt(self._namespace, i["request_id"]) != p.identity
                ):
                    raise CompensationError("conflict")
                await self._store.register_run(self._namespace, wf.run_id, i["request_id"], p.identity)
                check()
                return p.to_dict()

            return await self._execute(self._timeout, work)

        p = self._bound(
            await wf.step("recovery/prepare", prepare, retry=RetryPolicy(max_attempts=3, backoff="exponential"))
        )
        service = self

        class GuardedJob:
            async def submit(self, inputs: Any, *, options: Any) -> Any:
                try:
                    await service._execute(
                        options.timeout, lambda check, left: service._load_execution(p, wf.run_id, check, left)
                    )
                except Exception:
                    raise PipelineGateError("forbidden") from None
                return await service._job.job.submit(inputs, options=options)

            def resume(self, checkpoint: Any) -> Any:
                return service._job.job.resume(checkpoint)

        jobs = {
            "undo_" + str(n).zfill(2): PipelineJob(
                contract=self.worker_contract,
                job=cast(TypedJob[CompensationTask, CompensationOutcome[O]], GuardedJob()),
                job_store=self._job.job_store,
                parallelism=1,
                retry_max=self._job.retry_max if self._job.retry_max is not None else 2,
            )
            for n in range(20)
        }

        async def run(_input: CompensationInput, context: JobPipelineContext) -> CompensationResult[O]:
            decision = await context.approval(
                "review",
                self.review_contract,
                self._review(p),
                options=PipelineGateOptions(timeout=self._approval_timeout, required_roles=self._roles),
            )
            if decision.state != "approved":
                return dict(state=cast(Any, decision.state), results=[])
            results: list[CompensationResultItem[O]] = []
            for index, c in enumerate(p.commands):
                value = CompensationTask(plan_id=p.identity, command_id=c.id, recovery_run_id=wf.run_id)
                result = await context.job(
                    "undo_" + str(index).zfill(2),
                    [JobInput(effect_identity(p, c.effect_id), value)],
                    definition=jobs["undo_" + str(index).zfill(2)],
                )
                outcome = result.results[0].result
                results.append(dict(id=c.id, outcome=outcome))
                if outcome["state"] == "needs_review":
                    return dict(state="needs_review", results=results)
            return dict(state="completed", results=results)

        pipeline = JobPipeline(
            self.contract,
            scope=self._scope,
            store=self._job_store,
            jobs=jobs,
            gate_store=self._gates,
            run=run,
            options=JobPipelineOptions(request_timeout=self._timeout, poll_delay=self._delay, max_journal_bytes=262144),
        )
        return await pipeline.definition.run(envelope, wf)

    def handler(
        self,
        *,
        pool: EventOutboxPool,
        authorize: Callable[[EventOutboxTransaction, CompensationPlan[I], CompensationCommand[I]], Awaitable[bool]],
        apply: Callable[[EventOutboxTransaction, I, CompensationContext], Awaitable[CompensationOutcome[O]]],
    ) -> Callable[[CompensationTask, JobContext], Awaitable[CompensationOutcome[O]]]:
        async def handle(task: CompensationTask, context: JobContext) -> CompensationOutcome[O]:
            pending = False

            async def work(check: Callable[[], None], left: Callable[[], float]) -> CompensationOutcome[O]:
                nonlocal pending
                context.check_cancelled()
                self.worker_contract.input(task)
                p = self._bound(await self._stored(lambda: self._store.load(task["plan_id"]), check))
                c = next((c for c in p.commands if c.id == task["command_id"]), None)
                if c is None:
                    raise CompensationError("conflict")
                await self._load_execution(p, task["recovery_run_id"], check, left)
                key = effect_identity(p, c.effect_id)
                digest = job_value_hash([self._config, c.to_dict()])
                if context.input_id != key:
                    raise CompensationError("conflict")

                async def transaction(tx: EventOutboxTransaction) -> CompensationOutcome[O]:
                    await tx._query(SQL["lock"], ["compensation:" + key])
                    check()
                    context.check_cancelled()
                    if not await authorize(tx, p, c):
                        raise EventOutboxError("scope_mismatch")
                    check()
                    for prior in p.commands[: next(n for n, v in enumerate(p.commands) if v.id == c.id)]:
                        earlier = await tx._query(SQL["effect"], [effect_identity(p, prior.effect_id)])
                        if (
                            len(earlier) != 1
                            or earlier[0]["command_sha256"] != job_value_hash([self._config, prior.to_dict()])
                            or self.worker_contract.output(earlier[0]["outcome"])["state"] == "needs_review"
                        ):
                            raise EventOutboxError("conflict")
                    rows = await tx._query(SQL["effect"], [key])
                    if len(rows) > 1:
                        raise EventOutboxError("conflict")
                    if rows:
                        if rows[0]["command_sha256"] != digest:
                            raise EventOutboxError("conflict")
                        return self.worker_contract.output(rows[0]["outcome"])
                    await tx.execute("SAVEPOINT compensation_business")
                    outcome = self.worker_contract.output(
                        await apply(
                            tx,
                            self._business.input(c.input),
                            CompensationContext(effect_id=key, owner_id=p.owner_id, _check=check),
                        )
                    )
                    check()
                    context.check_cancelled()
                    if outcome["state"] == "needs_review":
                        await tx.execute("ROLLBACK TO SAVEPOINT compensation_business")
                    await tx.execute("RELEASE SAVEPOINT compensation_business")
                    saved = await tx._query(
                        SQL["complete"], [key, digest, json.dumps(outcome, ensure_ascii=False, separators=(",", ":"))]
                    )
                    if len(saved) != 1:
                        raise EventOutboxError("store_failed")
                    return self.worker_contract.output(saved[0]["outcome"])

                try:
                    pending = True
                    return await with_event_outbox_transaction(pool, transaction, timeout=left())
                except EventOutboxError as e:
                    raise CompensationError(
                        {
                            "scope_mismatch": "unauthorized",
                            "conflict": "conflict",
                            "commit_unknown": "commit_unknown",
                            "cancelled": "cancelled",
                            "deadline": "deadline",
                        }.get(e.code, "store_failed")
                    ) from None
                finally:
                    pending = False

            try:
                return await self._execute(self._timeout, work)
            except BaseException:
                if pending:
                    raise CompensationError("commit_unknown") from None
                raise

        return handle
