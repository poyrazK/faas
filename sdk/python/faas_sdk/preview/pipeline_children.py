"""Typed child workflow composition over the existing native workflow engine."""

from __future__ import annotations

from collections.abc import Awaitable, Callable, Sequence
from dataclasses import asdict, dataclass
from typing import Any, Generic, Literal, Protocol, TypeVar

from ..job_runtime import JobContract, JobInput, _scope, job_value_hash
from ..workflows import RetryPolicy, WorkflowContext
from .job_pipeline_client import JobPipelineClient, PipelineCheckpoint, PipelineInspection
from .live_jobs import LiveJobSnapshot, LiveJobWaiting
from .pipeline_children_state import (
    PipelineChildrenError,
    PipelineChildrenReceipt,
    PipelineChildrenStore,
    match_pipeline_child,
    pipeline_children_identity,
    pipeline_children_intent,
)
from .pipeline_gates import PipelineGateStore
from .pipeline_state import PipelineScope, _uuid

I = TypeVar("I")
O = TypeVar("O")
T = TypeVar("T")
CancelPolicy = Literal["leave_running", "cancel_children"]
StopReason = Literal["deadline", "child_failed", "parent_stopped"]
ChildPhase = Literal["unstarted", "submission_unresolved", "running", "succeeded", "failed", "cancelled", "skipped"]


@dataclass(frozen=True, kw_only=True)
class PipelineChild(Generic[I, O]):
    contract: JobContract[I, O]


@dataclass(frozen=True, kw_only=True)
class PipelineChildOptions:
    timeout: float
    concurrency: int = 1
    failure_policy: Literal["collect", "fail_fast"] = "collect"
    on_fail_fast: CancelPolicy = "leave_running"
    on_parent_stopped: CancelPolicy = "leave_running"
    on_deadline: CancelPolicy = "leave_running"


@dataclass(frozen=True, kw_only=True)
class PipelineChildView(Generic[O]):
    id: str
    phase: ChildPhase
    run_id: str | None
    cancel_requested: bool
    native_status: str | None = None
    output: O | None = None
    error: Literal["child_failed"] | None = None
    waiting: LiveJobWaiting | None = None

    def to_dict(self) -> dict[str, Any]:
        value = asdict(self)
        for key in ("native_status", "error", "waiting"):
            if value[key] is None:
                value.pop(key)
        if self.phase != "succeeded":
            value.pop("output")
        return value


@dataclass(frozen=True, kw_only=True)
class PipelineBatchResult(Generic[O]):
    identity: str
    parent_run_id: str
    batch: str
    stop_reason: StopReason | None
    complete: bool
    children: tuple[PipelineChildView[O], ...]

    def to_dict(self) -> dict[str, Any]:
        return {
            "identity": self.identity,
            "parent_run_id": self.parent_run_id,
            "batch": self.batch,
            "stop_reason": self.stop_reason,
            "complete": self.complete,
            "children": [c.to_dict() for c in self.children],
        }


class PipelineChildrenTransport(Protocol):
    async def parent(self, intent: dict[str, Any]) -> PipelineInspection: ...
    async def start(self, intent: dict[str, Any], index: int) -> PipelineCheckpoint: ...
    async def attach(self, intent: dict[str, Any], index: int, run_id: str) -> PipelineCheckpoint: ...
    async def observe(self, intent: dict[str, Any], index: int, checkpoint: PipelineCheckpoint) -> dict[str, Any]: ...
    async def cancel(self, intent: dict[str, Any], index: int, checkpoint: PipelineCheckpoint) -> None: ...


class NativePipelineChildrenTransport:
    def __init__(
        self,
        *,
        scope: PipelineScope,
        parent_contract: JobContract[Any, Any],
        children: Sequence[PipelineChild[Any, Any]],
        token: str,
        timeout: float = 10,
        **httpx_args: Any,
    ):
        self.scope, self.parent_contract, self.token, self.timeout, self.httpx_args = (
            scope,
            parent_contract,
            token,
            timeout,
            httpx_args,
        )
        self.contracts = {d.contract.sha256: d.contract for d in children}

    def _client(self, i: dict[str, Any], parent: bool = False) -> JobPipelineClient[Any, Any]:
        c = self.parent_contract if parent else self.contracts.get(i["child_sha256"])
        if (
            c is None
            or i["parent_sha256"] != self.parent_contract.sha256
            or i["scope"] != self.scope.to_dict()
            or not parent
            and c.spec["name"] != i["child_workflow"]
        ):
            raise PipelineChildrenError("forbidden")
        return JobPipelineClient(
            base_url=self.scope.base_url,
            scope=self.scope,
            token=self.token,
            owner_id=i["owner_id"],
            contract=c,
            **self.httpx_args,
        )

    async def parent(self, intent: dict[str, Any]) -> PipelineInspection:
        async with self._client(intent, True) as client:
            c = PipelineCheckpoint(
                version=1,
                run_id=intent["parent_run_id"],
                scope=self.scope,
                owner_id=intent["owner_id"],
                contract_sha256=intent["parent_sha256"],
                input_sha256=intent["parent_input_sha256"],
            )
            return await client.resume(c).inspect(timeout=self.timeout)

    async def start(self, intent: dict[str, Any], index: int) -> PipelineCheckpoint:
        async with self._client(intent) as client:
            return (
                await client.start(
                    intent["inputs"][index]["value"],
                    timeout=self.timeout,
                    idempotency_key=f"pipeline-child:{intent['identity']}:{index}",
                    lineage=dict(
                        version=1,
                        parent_run_id=intent["parent_run_id"],
                        batch_identity=intent["identity"],
                        item_id=intent["inputs"][index]["id"],
                    ),
                )
            ).checkpoint

    async def attach(self, intent: dict[str, Any], index: int, run_id: str) -> PipelineCheckpoint:
        async with self._client(intent) as client:
            return match_pipeline_child(
                intent, index, (await client.resume_by_id(run_id, timeout=self.timeout)).checkpoint
            )

    async def observe(self, intent: dict[str, Any], index: int, checkpoint: PipelineCheckpoint) -> dict[str, Any]:
        async with self._client(intent) as client:
            run = client.resume(match_pipeline_child(intent, index, checkpoint))
            p = await run.inspect(timeout=self.timeout)
            value: dict[str, Any] = dict(
                checkpoint=checkpoint.to_dict(), cancel_requested=False, native_status=p.status
            )
            if p.cancelled:
                return value | {"phase": "cancelled"}
            if p.status == "succeeded":
                return value | {"phase": "succeeded", "output": await run.result(timeout=self.timeout)}
            return value | {"phase": "failed" if p.status in ("failed", "dead") else "running"}

    async def cancel(self, intent: dict[str, Any], index: int, checkpoint: PipelineCheckpoint) -> None:
        async with self._client(intent) as client:
            await client.resume(match_pipeline_child(intent, index, checkpoint)).cancel(timeout=self.timeout)


class _ChildrenContentionError(RuntimeError):
    code = "contention"

    def __init__(self) -> None:
        super().__init__("pipeline-children:contention")


def _terminal(c: dict[str, Any]) -> bool:
    return c["phase"] in ("succeeded", "failed", "cancelled")


def _decode(value: dict[str, Any], intent: dict[str, Any], contract: JobContract[Any, O]) -> PipelineBatchResult[O]:
    try:
        if (
            value["identity"] != intent["identity"]
            or value["parent_run_id"] != intent["parent_run_id"]
            or value["batch"] != intent["batch"]
            or type(value["complete"]) is not bool
            or value["stop_reason"] not in (None, "deadline", "child_failed", "parent_stopped")
            or len(value["children"]) != len(intent["inputs"])
        ):
            raise ValueError
        children = []
        for n, c in enumerate(value["children"]):
            if (
                c["id"] != intent["inputs"][n]["id"]
                or c["phase"]
                not in ("unstarted", "submission_unresolved", "running", "succeeded", "failed", "cancelled", "skipped")
                or (c["phase"] in ("unstarted", "submission_unresolved", "skipped")) != (c["run_id"] is None)
                or c["run_id"] is not None
                and not _uuid(c["run_id"])
                or type(c["cancel_requested"]) is not bool
            ):
                raise ValueError
            children.append(
                PipelineChildView(
                    id=c["id"],
                    phase=c["phase"],
                    run_id=c["run_id"],
                    cancel_requested=c["cancel_requested"],
                    native_status=c.get("native_status"),
                    output=contract.output(c["output"]) if c["phase"] == "succeeded" else None,
                    error="child_failed" if c["phase"] == "failed" else None,
                )
            )
        return PipelineBatchResult(
            identity=value["identity"],
            parent_run_id=value["parent_run_id"],
            batch=value["batch"],
            stop_reason=value["stop_reason"],
            complete=value["complete"],
            children=tuple(children),
        )
    except Exception:
        raise PipelineChildrenError("conflict") from None


class PipelineChildren:
    def __init__(
        self,
        *,
        scope: PipelineScope,
        parent_contract: JobContract[Any, Any],
        children: Sequence[PipelineChild[Any, Any]],
        store: PipelineChildrenStore,
        transport: PipelineChildrenTransport | None = None,
        token: str | None = None,
        request_timeout: float = 10,
        gate_store: PipelineGateStore | None = None,
        **httpx_args: Any,
    ):
        self.scope, self.parent_contract, self.store, self.timeout, self.gate_store = (
            scope,
            parent_contract,
            store,
            request_timeout,
            gate_store,
        )
        self.contracts = {id(d): d.contract for d in children}
        if (
            not 1 <= len(self.contracts) <= 50
            or len({c.sha256 for c in self.contracts.values()}) != len(self.contracts)
            or transport is None
            and not token
            or not 0 < request_timeout <= 2147483.647
        ):
            raise PipelineChildrenError("invalid_input")
        self.transport = transport or NativePipelineChildrenTransport(
            scope=scope,
            parent_contract=parent_contract,
            children=children,
            token=token or "",
            timeout=request_timeout,
            **httpx_args,
        )

    def _contract(self, intent: dict[str, Any]) -> JobContract[Any, Any]:
        c = next(
            (
                c
                for c in self.contracts.values()
                if c.sha256 == intent["child_sha256"] and c.spec["name"] == intent["child_workflow"]
            ),
            None,
        )
        if (
            c is None
            or intent["parent_sha256"] != self.parent_contract.sha256
            or intent["scope"] != self.scope.to_dict()
        ):
            raise PipelineChildrenError("forbidden")
        return c

    def _owned(self, raw: PipelineChildrenReceipt, owner: str | None = None) -> PipelineChildrenReceipt:
        r = PipelineChildrenReceipt.from_dict(raw.to_dict())
        c = self._contract(r.intent)
        if owner is not None and owner != r.intent["owner_id"]:
            raise PipelineChildrenError("forbidden")
        for item in r.intent["inputs"]:
            c.input(item["value"])
        for child in r.state["children"]:
            if child["phase"] == "succeeded":
                c.output(child["output"])
        return r

    async def _execute(self, timeout: float, fn: Callable[[Callable[[], None]], Awaitable[T]]) -> T:
        async def safe(check: Callable[[], None]) -> T:
            try:
                result = await fn(check)
                check()
                return result
            except (PipelineChildrenError, _ChildrenContentionError):
                raise
            except Exception:
                check()
                raise RuntimeError("pipeline-children:request_failed") from None

        return await _scope(timeout, safe)

    async def _load(self, identity: str, check: Callable[[], None]) -> PipelineChildrenReceipt:
        raw = await self.store.load(identity)
        check()
        if raw is None or raw.intent["identity"] != identity:
            raise PipelineChildrenError("conflict")
        return self._owned(raw)

    async def _change(
        self,
        identity: str,
        fn: Callable[[PipelineChildrenReceipt], dict[str, Any] | None],
        check: Callable[[], None],
        admission: bool = False,
    ) -> tuple[bool, PipelineChildrenReceipt]:
        for _ in range(8):
            r = await self._load(identity, check)
            state = fn(r)
            check()
            if state is None:
                return False, r
            saved = await self.store.save(r, state, admission)
            check()
            if saved is not None:
                checked = self._owned(saved)
                if (
                    checked.revision != r.revision + 1
                    or job_value_hash(checked.intent) != job_value_hash(r.intent)
                    or job_value_hash(checked.state) != job_value_hash(state)
                ):
                    raise PipelineChildrenError("conflict")
                return True, checked
        raise _ChildrenContentionError()

    async def _complete(
        self, intent: dict[str, Any], index: int, checkpoint: PipelineCheckpoint, check: Callable[[], None]
    ) -> None:
        checkpoint = match_pipeline_child(intent, index, checkpoint)

        def change(r: PipelineChildrenReceipt) -> dict[str, Any] | None:
            s = r.state
            c = s["children"][index]
            if c["checkpoint"] is not None:
                if job_value_hash(c["checkpoint"]) != job_value_hash(checkpoint.to_dict()):
                    raise PipelineChildrenError("conflict")
                return None
            if c["phase"] != "submission_unresolved":
                raise PipelineChildrenError("conflict")
            s["children"][index] = c | {"phase": "running", "checkpoint": checkpoint.to_dict()}
            return s

        await self._change(intent["identity"], change, check)

    async def _round(
        self, identity: str, check: Callable[[], None], admit: bool, guard: Callable[[], None]
    ) -> PipelineChildrenReceipt:
        r = await self._load(identity, check)
        parent = await self.transport.parent(r.intent)
        check()
        stopped = parent.cancelled or parent.status in ("succeeded", "failed", "dead")

        def control(current: PipelineChildrenReceipt) -> dict[str, Any] | None:
            s = current.state
            stop = s["stop_reason"] or ("parent_stopped" if stopped else "deadline" if current.expired else None)
            if stop != s["stop_reason"] or stopped and not s["parent_stopped"]:
                return s | {"stop_reason": stop, "parent_stopped": s["parent_stopped"] or stopped}
            return None

        await self._change(identity, control, check)
        for n in range(len(r.intent["inputs"])):
            r = await self._load(identity, check)
            child = r.state["children"][n]
            if child["phase"] != "running":
                continue
            observed = await self.transport.observe(r.intent, n, PipelineCheckpoint.from_dict(child["checkpoint"]))
            check()
            match_pipeline_child(r.intent, n, observed["checkpoint"])
            if observed["phase"] == "succeeded":
                self._contract(r.intent).output(observed["output"])

            def observation(current: PipelineChildrenReceipt) -> dict[str, Any] | None:
                s = current.state
                old = s["children"][n]
                if _terminal(old):
                    return None
                if job_value_hash(old["checkpoint"]) != job_value_hash(observed["checkpoint"]):
                    raise PipelineChildrenError("conflict")
                s["children"][n] = observed | {"cancel_requested": old["cancel_requested"]}
                s["stop_reason"] = s["stop_reason"] or (
                    "child_failed"
                    if observed["phase"] in ("failed", "cancelled") and current.intent["failure_policy"] == "fail_fast"
                    else None
                )
                return s

            await self._change(identity, observation, check)
        r = await self._load(identity, check)
        i, s = r.intent, r.state
        cancel = (
            s["parent_stopped"]
            and i["on_parent_stopped"] == "cancel_children"
            or s["stop_reason"] == "child_failed"
            and i["on_fail_fast"] == "cancel_children"
            or s["stop_reason"] == "deadline"
            and i["on_deadline"] == "cancel_children"
        )
        if cancel:
            for n in range(len(i["inputs"])):
                r = await self._load(identity, check)
                child = r.state["children"][n]
                if child["phase"] != "running":
                    continue

                def requesting(current: PipelineChildrenReceipt) -> dict[str, Any] | None:
                    state = current.state
                    if state["children"][n]["cancel_requested"]:
                        return None
                    state["children"][n]["cancel_requested"] = True
                    return state

                await self._change(identity, requesting, check)
                await self.transport.cancel(r.intent, n, PipelineCheckpoint.from_dict(child["checkpoint"]))
                check()
        if admit:
            for n in range(len(i["inputs"])):
                guard()
                check()

                def reserve(current: PipelineChildrenReceipt) -> dict[str, Any] | None:
                    state = current.state
                    if (
                        current.expired
                        or state["stop_reason"]
                        or state["children"][n]["phase"] != "unstarted"
                        or sum(c["phase"] in ("running", "submission_unresolved") for c in state["children"])
                        >= current.intent["concurrency"]
                    ):
                        return None
                    state["children"][n] = dict(phase="submission_unresolved", checkpoint=None, cancel_requested=False)
                    return state

                created, saved = await self._change(identity, reserve, check, True)
                if not created:
                    continue
                guard()
                check()
                current = await self._load(identity, check)
                if current.expired or current.state["stop_reason"]:

                    def retire(latest: PipelineChildrenReceipt) -> dict[str, Any] | None:
                        state = latest.state
                        if state["children"][n]["phase"] != "submission_unresolved":
                            return None
                        state["stop_reason"] = state["stop_reason"] or "deadline"
                        state["children"][n] = dict(phase="unstarted", checkpoint=None, cancel_requested=False)
                        return state

                    await self._change(identity, retire, check)
                    continue
                if current.state["children"][n]["phase"] != "submission_unresolved":
                    continue
                try:
                    checkpoint = await self.transport.start(saved.intent, n)
                    check()
                    await self._complete(saved.intent, n, checkpoint, check)
                except (PipelineChildrenError, _ChildrenContentionError):
                    raise
                except Exception:
                    check()  # uncertain accepted child is never submitted again
        return await self._load(identity, check)

    def _snapshot(self, r: PipelineChildrenReceipt, contract: JobContract[Any, O]) -> PipelineBatchResult[O]:
        self._owned(r)
        i, s = r.intent, r.state
        stop = s["stop_reason"] or ("deadline" if r.expired else None)
        children = []
        for n, c in enumerate(s["children"]):
            value: dict[str, Any] = dict(
                id=i["inputs"][n]["id"],
                phase="skipped" if c["phase"] == "unstarted" and stop else c["phase"],
                run_id=c["checkpoint"]["run_id"] if c["checkpoint"] else None,
                cancel_requested=c["cancel_requested"],
            )
            if c.get("native_status"):
                value["native_status"] = c["native_status"]
            if c["phase"] == "succeeded":
                value["output"] = contract.output(c["output"])
            if c["phase"] == "failed":
                value["error"] = "child_failed"
            children.append(value)
        return _decode(
            dict(
                identity=i["identity"],
                parent_run_id=i["parent_run_id"],
                batch=i["batch"],
                stop_reason=stop,
                complete=all(_terminal(c) or c["phase"] == "unstarted" and bool(stop) for c in s["children"]),
                children=children,
            ),
            i,
            contract,
        )

    async def run(
        self,
        wf: WorkflowContext,
        envelope: dict[str, Any],
        name: str,
        definition: PipelineChild[I, O],
        inputs: Sequence[JobInput[I]],
        options: PipelineChildOptions,
        *,
        claim: Callable[[], None],
        budget: Callable[[Any], None],
        guard: Callable[[], None],
        polls: int,
        delay: float,
    ) -> PipelineBatchResult[O]:
        c = self.contracts.get(id(definition))
        if (
            c is None
            or envelope["contract_sha256"] != self.parent_contract.sha256
            or envelope["scope"] != self.scope.to_dict()
        ):
            raise PipelineChildrenError("forbidden")
        milliseconds = options.timeout * 1000
        if (
            type(options.timeout) not in (int, float)
            or not 0 < milliseconds <= 86400000
            or milliseconds != int(milliseconds)
        ):
            raise PipelineChildrenError("invalid_input")
        values = []
        for item in inputs:
            value = c.input(item.value)
            values.append(
                dict(
                    id=item.id,
                    value=value,
                    input_sha256=job_value_hash(
                        dict(
                            lineage=dict(
                                version=1,
                                parent_run_id=wf.run_id,
                                batch_identity=pipeline_children_identity(self.scope, wf.run_id, name),
                                item_id=item.id,
                            ),
                            version=1,
                            scope=self.scope.to_dict(),
                            owner_id=envelope["owner_id"],
                            contract_sha256=c.sha256,
                            input=value,
                        )
                    ),
                )
            )
        intent = pipeline_children_intent(
            scope=self.scope.to_dict(),
            parent_run_id=wf.run_id,
            owner_id=envelope["owner_id"],
            batch=name,
            parent_sha256=self.parent_contract.sha256,
            parent_input_sha256=job_value_hash(envelope | {"input": self.parent_contract.input(envelope["input"])}),
            child_sha256=c.sha256,
            child_workflow=c.spec["name"],
            inputs=values,
            concurrency=options.concurrency,
            timeout_ms=int(milliseconds),
            failure_policy=options.failure_policy,
            on_fail_fast=options.on_fail_fast,
            on_parent_stopped=options.on_parent_stopped,
            on_deadline=options.on_deadline,
        )
        binding = job_value_hash(intent)

        async def opening(_: Any) -> dict[str, str]:
            async def work(check: Callable[[], None]) -> dict[str, str]:
                guard()
                await self.transport.parent(intent)
                check()
                r = self._owned(await self.store.open(intent))
                check()
                if job_value_hash(r.intent) != binding:
                    raise PipelineChildrenError("conflict")
                return dict(identity=intent["identity"], binding_sha256=binding)

            return await self._execute(self.timeout, work)

        claim()
        opened = await wf.step(
            "pipeline-children/" + name + "/open", opening, retry=RetryPolicy(max_attempts=3, backoff="exponential")
        )
        if opened != dict(identity=intent["identity"], binding_sha256=binding):
            raise PipelineChildrenError("conflict")
        budget(opened)
        for _ in range(polls):

            async def round_step(_: Any) -> dict[str, Any]:
                async def work(check: Callable[[], None]) -> dict[str, Any]:
                    r = await self._load(intent["identity"], check)
                    if job_value_hash(r.intent) != binding:
                        raise PipelineChildrenError("conflict")
                    view = self._snapshot(await self._round(intent["identity"], check, True, guard), c)
                    return (
                        view.to_dict()
                        if view.complete or view.stop_reason
                        else dict(identity=intent["identity"], complete=False, stop_reason=None)
                    )

                return await self._execute(self.timeout, work)

            claim()
            raw = await wf.step(
                "pipeline-children/" + name + "/round",
                round_step,
                retry=RetryPolicy(max_attempts=3, backoff="exponential"),
            )
            if raw.get("identity") != intent["identity"]:
                raise PipelineChildrenError("conflict")
            budget(raw)
            if "children" in raw:
                result: PipelineBatchResult[O] = _decode(raw, intent, c)
                if result.complete or result.stop_reason:
                    return result
            elif raw.get("complete") is not False or raw.get("stop_reason") is not None:
                raise PipelineChildrenError("conflict")
            claim()
            await wf.sleep("pipeline-children/" + name + "/wait", delay)
        raise PipelineChildrenError("poll_limit")

    async def inspect(
        self, parent_run_id: str, batch: str, owner: str, definition: PipelineChild[Any, O], *, timeout: float
    ) -> PipelineBatchResult[O]:
        async def work(check: Callable[[], None]) -> PipelineBatchResult[O]:
            r = self._owned(
                await self._load(pipeline_children_identity(self.scope, parent_run_id, batch), check), owner
            )
            if self.contracts.get(id(definition)) is None or definition.contract.sha256 != r.intent["child_sha256"]:
                raise PipelineChildrenError("forbidden")
            await self.transport.parent(r.intent)
            check()
            state = r.state
            for n, child in enumerate(state["children"]):
                if child["checkpoint"] is not None and not _terminal(child):
                    state["children"][n] = await self.transport.observe(
                        r.intent, n, PipelineCheckpoint.from_dict(child["checkpoint"])
                    ) | {"cancel_requested": child["cancel_requested"]}
                    check()
            view = self._snapshot(
                PipelineChildrenReceipt.from_dict(r.to_dict() | {"state": state}), definition.contract
            )
            detailed = []
            for child_view in view.children:
                waiting = None
                if self.gate_store is not None and child_view.run_id and child_view.native_status == "awaiting_event":
                    rows = await self.gate_store.list(child_view.run_id, owner)
                    check()
                    gate = next(
                        (
                            g
                            for g in rows
                            if g.decision is None
                            and g.intent["pipeline_sha256"] == r.intent["child_sha256"]
                            and g.intent["owner_id"] == owner
                            and g.intent["scope"] == self.scope.to_dict()
                        ),
                        None,
                    )
                    if gate:
                        waiting = LiveJobWaiting(
                            kind=gate.intent["kind"], gate_id=gate.intent["id"], name=gate.intent["name"]
                        )
                from dataclasses import replace

                detailed.append(replace(child_view, waiting=waiting))
            from dataclasses import replace

            return replace(view, children=tuple(detailed))

        return await self._execute(timeout, work)

    async def recover(self, limit: int, *, timeout: float) -> int:
        async def work(check: Callable[[], None]) -> int:
            if type(limit) is not int or not 1 <= limit <= 100:
                raise PipelineChildrenError("invalid_input")
            rows = await self.store.scan(self.scope, self.parent_contract.sha256, limit)
            check()
            if len(rows) > limit:
                raise PipelineChildrenError("conflict")
            for row in rows:
                self._owned(row)
                await self._round(row.intent["identity"], check, False, lambda: None)
                check()
            return len(rows)

        return await self._execute(timeout, work)

    async def reconcile(
        self, parent_run_id: str, batch: str, item_id: str, child_run_id: str, owner: str, *, timeout: float
    ) -> None:
        async def work(check: Callable[[], None]) -> None:
            r = self._owned(
                await self._load(pipeline_children_identity(self.scope, parent_run_id, batch), check), owner
            )
            n = next((n for n, item in enumerate(r.intent["inputs"]) if item["id"] == item_id), -1)
            if n < 0:
                raise PipelineChildrenError("invalid_input")
            await self.transport.parent(r.intent)
            check()
            c = await self.transport.attach(r.intent, n, child_run_id)
            check()
            await self._complete(r.intent, n, c, check)

        await self._execute(timeout, work)


def pipeline_children_live_snapshot(
    view: PipelineBatchResult[Any], parent: PipelineInspection, resource_id: str
) -> LiveJobSnapshot:
    if view.parent_run_id != parent.run_id:
        raise PipelineChildrenError("conflict")
    status = (
        "cancelled"
        if parent.cancelled
        else "dead_letter"
        if parent.status == "dead"
        else parent.status
        if parent.status in ("succeeded", "failed")
        else "running"
    )
    value: dict[str, Any] = dict(
        version=1,
        resource_id=resource_id,
        run_id=parent.run_id,
        status=status,
        total=len(view.children),
        succeeded=sum(c.phase == "succeeded" for c in view.children),
        failed=sum(c.phase == "failed" for c in view.children),
        cancelled=sum(c.phase == "cancelled" for c in view.children),
        running=sum(c.phase == "running" for c in view.children),
        failures=[dict(input_id=c.id, code="child_failed") for c in view.children if c.phase == "failed"],
        report="none",
    )
    waiting = next((c.waiting for c in view.children if c.waiting), None)
    if waiting and parent.status not in ("succeeded", "failed", "dead") and not parent.cancelled:
        value["waiting"] = asdict(waiting)
    return LiveJobSnapshot.from_dict(value, resource_id)
