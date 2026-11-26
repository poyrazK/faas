"""Typed Jobs composed using Gregale's existing durable workflow journal."""

from __future__ import annotations

import copy
import json
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any, Generic, TypeVar

from .._typed_jobs import JobProgress, JobRunOptions, JobSubmitOptions, TypedJob
from ..job_runtime import (
    JobContext,
    JobContract,
    JobInput,
    JobStore,
    job_hash,
    job_input_id,
    job_value_hash,
    prepare_job_input,
)
from ..job_runtime import (
    _scope as _job_scope,
)
from ..workflows import RetryPolicy, WorkflowContext, WorkflowDefinition
from .pipeline_children import (
    PipelineBatchResult,
    PipelineChild,
    PipelineChildOptions,
    PipelineChildren,
    PipelineChildView,
)
from .pipeline_children_state import PipelineChildrenError
from .pipeline_gates import (
    GateKind,
    PipelineGateError,
    PipelineGateOptions,
    PipelineGateOutcome,
    PipelineGateStore,
    pipeline_gate_intent,
    run_pipeline_gate,
)
from .pipeline_state import (
    PinnedPipelineSource,
    PipelineError,
    PipelineJobReceipt,
    PipelineJobStore,
    PipelineScope,
    _parse_pinned_source,
    _parse_pipeline_lineage,
    _parse_pipeline_trigger,
    match_pipeline_job,
    pipeline_job_identity,
    pipeline_name,
)

I = TypeVar("I")
O = TypeVar("O")
PI = TypeVar("PI")
PO = TypeVar("PO")
T = TypeVar("T")


async def _scope(timeout: float, work: Callable[[Callable[[], None]], Awaitable[T]]) -> T:
    async def safe(check: Callable[[], None]) -> T:
        try:
            return await work(check)
        except (PipelineError, PipelineGateError):
            raise
        except Exception:
            check()
            raise RuntimeError("pipeline:request_failed") from None

    return await _job_scope(timeout, safe)


def _bytes(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode()


@dataclass(frozen=True, kw_only=True)
class PipelineJob(Generic[I, O]):
    contract: JobContract[I, O]
    job: TypedJob[I, O]
    job_store: JobStore | None = None
    parallelism: int | None = None
    retry_max: int | None = None

    def __post_init__(self) -> None:
        if (
            self.parallelism is not None
            and (type(self.parallelism) is not int or self.parallelism < 1)
            or self.retry_max is not None
            and (type(self.retry_max) is not int or self.retry_max < 0)
        ):
            raise PipelineError("invalid_input")


@dataclass(frozen=True, kw_only=True)
class PipelineItem(Generic[O]):
    input_id: str
    index: int
    attempt: int
    result: O


@dataclass(frozen=True, kw_only=True)
class PipelineJobResult(Generic[O]):
    run_id: str
    results: tuple[PipelineItem[O], ...]


@dataclass(frozen=True, kw_only=True)
class JobPipelineOptions:
    request_timeout: float = 10
    poll_delay: float = 1
    max_polls: int = 100
    max_operations: int = 900
    max_journal_bytes: int = 65536

    def __post_init__(self) -> None:
        import math

        for value in (self.request_timeout, self.poll_delay):
            if type(value) not in (int, float) or not math.isfinite(value) or not 0 < value <= 2147483.647:
                raise PipelineError("invalid_input")
        for value, maximum in ((self.max_polls, 150), (self.max_operations, 950), (self.max_journal_bytes, 262144)):
            if type(value) is not int or not 1 <= value <= maximum:
                raise PipelineError("invalid_input")


class JobPipelineContext:
    def __init__(
        self, pipeline: JobPipeline[Any, Any], workflow: WorkflowContext, owner_id: str, input_sha256: str | None = None
    ):
        self._pipeline, self._wf, self.owner_id = pipeline, workflow, owner_id
        self.run_id = workflow.run_id
        self._used: set[str] = set()
        self._operations = self._journal_bytes = self._pending_gates = 0
        self._blocked = False
        self._input_sha256 = input_sha256
        self._envelope: dict[str, Any] | None = None
        self.pinned_source: PinnedPipelineSource | None = None
        self._composing = False

    def _claim(self) -> None:
        self._operations += 1
        if self._operations > self._pipeline.options.max_operations:
            raise PipelineError("operation_limit")

    def _budget(self, value: Any) -> None:
        self._journal_bytes += len(_bytes(value))
        if self._journal_bytes > self._pipeline.options.max_journal_bytes:
            raise PipelineError("journal_limit")

    def _can_submit(self) -> None:
        if self._pending_gates or self._blocked:
            raise PipelineGateError("blocked")

    async def approval(
        self, name: str, contract: JobContract[I, O], review: I, *, options: PipelineGateOptions
    ) -> PipelineGateOutcome[O]:
        return await self._gate("approval", name, contract, review, options)

    async def wait_for_input(
        self, name: str, contract: JobContract[I, O], review: I, *, options: PipelineGateOptions
    ) -> PipelineGateOutcome[O]:
        return await self._gate("input", name, contract, review, options)

    async def _gate(
        self, kind: GateKind, name: str, contract: JobContract[I, O], review: I, options: PipelineGateOptions
    ) -> PipelineGateOutcome[O]:
        p = self._pipeline
        if self._composing:
            raise PipelineChildrenError("blocked")
        if p.gate_store is None or name in self._used:
            raise PipelineGateError("invalid_input")
        self._used.add(name)
        self._pending_gates += 1
        try:
            milliseconds = options.timeout * 1000
            if (
                type(options.timeout) not in (int, float)
                or not 0 < milliseconds <= 86400000
                or milliseconds != int(milliseconds)
            ):
                raise PipelineGateError("invalid_input")
            intent = pipeline_gate_intent(
                kind=kind,
                name=name,
                scope=p.scope.to_dict(),
                workflow_run_id=self.run_id,
                owner_id=self.owner_id,
                pipeline_sha256=p.contract.sha256,
                input_sha256=self._input_sha256,
                contract_sha256=contract.sha256,
                review=contract.input(review),
                required_roles=options.required_roles,
                timeout_ms=int(milliseconds),
            )
            outcome = await run_pipeline_gate(
                self._wf, p.gate_store, intent, contract, p.options.request_timeout, self._claim, self._budget
            )
            if outcome.state not in ("approved", "submitted"):
                self._blocked = True
            return outcome
        except BaseException:
            self._blocked = True
            raise
        finally:
            self._pending_gates -= 1

    async def children(
        self,
        name: str,
        definition: PipelineChild[I, O],
        inputs: Sequence[JobInput[I]],
        *,
        options: PipelineChildOptions,
    ) -> PipelineBatchResult[O]:
        self._can_submit()
        p = self._pipeline
        if p.children is None or self._envelope is None or name in self._used or self._composing:
            raise PipelineError("invalid_input")
        self._used.add(name)
        self._composing = True
        try:
            return await p.children.run(
                self._wf,
                self._envelope,
                name,
                definition,
                inputs,
                options,
                claim=self._claim,
                budget=self._budget,
                guard=self._can_submit,
                polls=p.options.max_polls,
                delay=p.options.poll_delay,
            )
        finally:
            self._composing = False

    async def child(
        self, name: str, definition: PipelineChild[I, O], value: I, *, options: PipelineChildOptions
    ) -> PipelineChildView[O]:
        from dataclasses import replace

        result = await self.children(
            name, definition, [JobInput("one", value)], options=replace(options, concurrency=1)
        )
        return result.children[0]

    async def job(
        self, step: str, inputs: Sequence[JobInput[I]], *, definition: PipelineJob[I, O]
    ) -> PipelineJobResult[O]:
        self._can_submit()
        if self._composing:
            raise PipelineChildrenError("blocked")
        p, wf, settings = self._pipeline, self._wf, self._pipeline.options
        if step not in p.jobs or p.jobs[step] is not definition or step in self._used:
            raise PipelineError("invalid_input", step)
        self._used.add(step)
        if not 1 <= len(inputs) <= 1000 or any(not job_input_id(i.id) for i in inputs):
            raise PipelineError("invalid_input", step)
        captured = tuple(JobInput(i.id, definition.contract.input(i.value)) for i in inputs)
        if not 1 <= len(captured) <= 1000 or len({i.id for i in captured}) != len(captured):
            raise PipelineError("invalid_input", step)
        values_hash = job_value_hash(
            {
                "inputs": [{"id": i.id, "value": i.value} for i in captured],
                "parallelism": definition.parallelism,
                "retry_max": definition.retry_max,
            }
        )
        identity = pipeline_job_identity(p.scope, wf.run_id, step)
        submitted_value = {
            "identity": identity,
            "values_sha256": values_hash,
            "contract_sha256": definition.contract.sha256,
        }

        async def submit(_: Any) -> dict[str, str]:
            async def work(check: Callable[[], None]) -> dict[str, str]:
                old = await p._load(wf.run_id, step, self.owner_id, check)
                if old is not None:
                    if old.intent["values_sha256"] != values_hash:
                        raise PipelineError("receipt_conflict", step)
                    return submitted_value
                refs = []
                for item in captured:
                    check()
                    ref = await prepare_job_input(
                        definition.contract,
                        item,
                        JobContext(input_id=item.id, attempt=0, _check=check),
                        definition.job_store,
                    )
                    check()
                    refs.append(ref.to_dict())
                if len(_bytes(refs)) > 1024 * 1024:
                    raise PipelineError("invalid_input", step)
                intent = {
                    "version": 1,
                    "identity": identity,
                    "scope": p.scope.to_dict(),
                    "workflow_run_id": wf.run_id,
                    "step": step,
                    "owner_id": self.owner_id,
                    "pipeline_sha256": p.contract.sha256,
                    "contract_sha256": definition.contract.sha256,
                    "job_name": definition.contract.spec["name"],
                    "values_sha256": values_hash,
                    "input_digest": job_hash(_bytes(refs)),
                    "idempotency_key": "pipeline-job:" + identity,
                    "inputs": [
                        {
                            "input_id": r["input_id"],
                            "binding_sha256": job_hash(r["input_ref"].encode()),
                            "source_task_index": None,
                        }
                        for r in refs
                    ],
                }
                created, raw = await p.store.reserve(copy.deepcopy(intent))
                check()
                saved = p._owned(PipelineJobReceipt.from_dict(raw.to_dict()), wf.run_id, step, self.owner_id)
                if (
                    type(created) is not bool
                    or saved.intent["values_sha256"] != values_hash
                    or created
                    and job_value_hash(saved.intent) != job_value_hash(intent)
                ):
                    raise PipelineError("receipt_conflict", step)
                if created:
                    if saved.checkpoint is not None:
                        raise PipelineError("receipt_conflict", step)
                    check()
                    try:
                        self._can_submit()
                        accepted = await definition.job.submit(
                            list(captured),
                            options=JobSubmitOptions(
                                timeout=settings.request_timeout,
                                idempotency_key=intent["idempotency_key"],
                                expected_base_url=p.scope.base_url,
                                expected_account_id=p.scope.account_id,
                                expected_contract_sha256=definition.contract.sha256,
                                max_items=1000,
                                parallelism=definition.parallelism,
                                retry_max=definition.retry_max,
                            ),
                        )
                        check()
                        checkpoint = match_pipeline_job(intent, accepted.checkpoint)
                        done = await p.store.complete(intent, checkpoint)
                        check()
                        if job_value_hash(done.to_dict()) != job_value_hash(
                            {"intent": intent, "checkpoint": checkpoint.to_dict()}
                        ):
                            raise PipelineError("receipt_conflict", step)
                    except (PipelineError, PipelineGateError):
                        raise
                    except Exception:
                        check()  # uncertain acceptance stays in the durable intent
                return submitted_value

            return await _scope(settings.request_timeout, work)

        self._claim()
        submitted = await wf.step(
            "pipeline/" + step + "/submit", submit, retry=RetryPolicy(max_attempts=3, backoff="exponential")
        )
        if job_value_hash(submitted) != job_value_hash(submitted_value):
            raise PipelineError("receipt_conflict", step)
        self._budget(submitted)

        async def observe(_: Any) -> dict[str, Any]:
            async def work(check: Callable[[], None]) -> dict[str, Any]:
                receipt = await p._load(wf.run_id, step, self.owner_id, check)
                if receipt is None or receipt.intent["values_sha256"] != values_hash:
                    raise PipelineError("receipt_conflict", step)
                if receipt.checkpoint is None:
                    return {"state": "submission_unresolved"}
                handle = definition.job.resume(receipt.checkpoint)
                progress = await handle.status(options=JobRunOptions(timeout=settings.request_timeout))
                check()
                if progress.status not in ("succeeded", "failed", "cancelled", "dead_letter"):
                    return {"state": "running"}
                if progress.status != "succeeded":
                    raise PipelineError("job_failed", step, progress.run_id)
                results = await handle.results(options=JobRunOptions(timeout=settings.request_timeout, max_items=1000))
                check()
                if len(results) != len(captured) or any(r.outcome != "succeeded" for r in results):
                    raise PipelineError("invalid_result", step, progress.run_id)
                value = {
                    "state": "succeeded",
                    "result": {
                        "run_id": progress.run_id,
                        "results": [
                            {
                                "input_id": r.input_id,
                                "index": r.index,
                                "attempt": r.attempt,
                                "result": definition.contract.output(r.result),
                            }
                            for r in results
                        ],
                    },
                }
                if len(_bytes(value)) > settings.max_journal_bytes:
                    raise PipelineError("journal_limit", step)
                return value

            return await _scope(settings.request_timeout, work)

        for _ in range(settings.max_polls):
            self._claim()
            state = await wf.step(
                "pipeline/" + step + "/observe", observe, retry=RetryPolicy(max_attempts=3, backoff="exponential")
            )
            self._budget(state)
            if state["state"] == "succeeded":
                result = state["result"]
                if len(result["results"]) != len(captured) or any(
                    r["input_id"] != captured[n].id
                    or r["index"] != n
                    or type(r["attempt"]) is not int
                    or r["attempt"] < 1
                    for n, r in enumerate(result["results"])
                ):
                    raise PipelineError("invalid_result", step)
                return PipelineJobResult(
                    run_id=result["run_id"],
                    results=tuple(
                        PipelineItem(
                            input_id=r["input_id"],
                            index=r["index"],
                            attempt=r["attempt"],
                            result=definition.contract.output(r["result"]),
                        )
                        for r in result["results"]
                    ),
                )
            self._claim()
            await wf.sleep("pipeline/" + step + "/wait", settings.poll_delay)
        raise PipelineError("poll_limit", step)


class JobPipeline(Generic[PI, PO]):
    def __init__(
        self,
        contract: JobContract[PI, PO],
        *,
        scope: PipelineScope,
        store: PipelineJobStore,
        jobs: Mapping[str, PipelineJob[Any, Any]],
        run: Callable[[PI, JobPipelineContext], Awaitable[PO]],
        options: JobPipelineOptions | None = None,
        gate_store: PipelineGateStore | None = None,
        children: PipelineChildren | None = None,
    ):
        self.contract, self.scope, self.store = contract, scope, store
        self.gate_store = gate_store
        self.children = children
        self.jobs, self.options = dict(jobs), options or JobPipelineOptions()
        if not self.jobs and gate_store is None and children is None or any(not pipeline_name(k) for k in self.jobs):
            raise PipelineError("invalid_input")

        async def entry(envelope: Any, wf: WorkflowContext) -> dict[str, Any]:
            if (
                not isinstance(envelope, dict)
                or envelope.get("version") != 1
                or envelope.get("contract_sha256") != contract.sha256
                or not pipeline_name(envelope.get("owner_id"))
                or envelope.get("scope") != scope.to_dict()
            ):
                raise PipelineError("scope_mismatch")
            if "lineage" in envelope:
                _parse_pipeline_lineage(envelope["lineage"])
            if "trigger" in envelope:
                _parse_pipeline_trigger(envelope["trigger"])
            value = contract.input(envelope["input"])
            context = JobPipelineContext(self, wf, envelope["owner_id"], job_value_hash(envelope | {"input": value}))
            context._envelope = envelope | {"input": value}
            if "pinned_source" in envelope:
                context.pinned_source = _parse_pinned_source(
                    envelope["pinned_source"], scope.base_url, scope.app_slug, envelope["owner_id"],
                )
            output = contract.output(await run(value, context))
            if context._pending_gates:
                raise PipelineGateError("pending")
            if context._composing:
                context._blocked = True
                raise PipelineChildrenError("blocked")
            context._budget(output)
            return {
                "version": 1,
                "contract_sha256": contract.sha256,
                "scope": scope.to_dict(),
                "owner_id": envelope["owner_id"],
                "output": output,
            }

        self.definition = WorkflowDefinition(name=contract.spec["name"], run=entry)

    def _owned(self, receipt: PipelineJobReceipt, run_id: str, step: str, owner_id: str) -> PipelineJobReceipt:
        i = receipt.intent
        if (
            not pipeline_name(owner_id)
            or i["identity"] != pipeline_job_identity(self.scope, run_id, step)
            or i["workflow_run_id"] != run_id
            or i["step"] != step
            or i["owner_id"] != owner_id
            or i["pipeline_sha256"] != self.contract.sha256
            or i["scope"] != self.scope.to_dict()
            or i["contract_sha256"] != self.jobs[step].contract.sha256
            or i["job_name"] != self.jobs[step].contract.spec["name"]
        ):
            raise PipelineError("receipt_conflict", step)
        return receipt

    async def _load(
        self, run_id: str, step: str, owner_id: str, check: Callable[[], None]
    ) -> PipelineJobReceipt | None:
        raw = await self.store.load(pipeline_job_identity(self.scope, run_id, step))
        check()
        return (
            self._owned(PipelineJobReceipt.from_dict(raw.to_dict()), run_id, step, owner_id)
            if raw is not None
            else None
        )

    async def reconcile(
        self, workflow_run_id: str, step: str, job_run_id: str, owner_id: str, *, timeout: float
    ) -> PipelineJobReceipt:
        if step not in self.jobs:
            raise PipelineError("invalid_input")

        async def work(check: Callable[[], None]) -> PipelineJobReceipt:
            receipt = await self._load(workflow_run_id, step, owner_id, check)
            if receipt is None:
                raise PipelineError("invalid_input")
            accepted = await self.jobs[step].job.resume_by_id(
                job_run_id, options=JobRunOptions(timeout=timeout, max_items=1000)
            )
            check()
            checkpoint = match_pipeline_job(receipt.intent, accepted.checkpoint)
            saved = await self.store.complete(receipt.intent, checkpoint)
            check()
            verified = self._owned(PipelineJobReceipt.from_dict(saved.to_dict()), workflow_run_id, step, owner_id)
            if (
                verified.checkpoint is None
                or verified.checkpoint.run_id != job_run_id
                or job_value_hash(verified.intent) != job_value_hash(receipt.intent)
            ):
                raise PipelineError("receipt_conflict")
            return verified

        return await _scope(timeout, work)

    async def inspect_job(
        self, workflow_run_id: str, step: str, owner_id: str, *, timeout: float
    ) -> tuple[PipelineJobReceipt | None, JobProgress | None]:
        if step not in self.jobs:
            raise PipelineError("invalid_input")

        async def work(check: Callable[[], None]) -> tuple[PipelineJobReceipt | None, JobProgress | None]:
            receipt = await self._load(workflow_run_id, step, owner_id, check)
            progress = (
                await self.jobs[step].job.resume(receipt.checkpoint).status(options=JobRunOptions(timeout=timeout))
                if receipt is not None and receipt.checkpoint is not None
                else None
            )
            check()
            return receipt, progress

        return await _scope(timeout, work)
