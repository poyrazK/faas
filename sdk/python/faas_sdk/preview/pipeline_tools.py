"""Typed durable business tools over existing native Job pipelines and approval gates."""

from __future__ import annotations

import asyncio
import copy
from collections.abc import Awaitable, Callable
from dataclasses import asdict, dataclass
from typing import Any, Generic, Literal, TypeVar, cast
from urllib.parse import urlencode, urlsplit, urlunsplit

from ..job_runtime import JobContract, JobError, _scope, job_value_hash
from .event_pipeline_state import event_pipeline_scope
from .job_pipeline_client import JobPipelineClient, PipelineStepInspection
from .pipeline_gates import PipelineGateRecord, PipelineGateStore
from .pipeline_state import PipelineScope, pipeline_name
from .pipeline_tool_state import (
    PipelineToolAction,
    PipelineToolActor,
    PipelineToolError,
    PipelineToolReceipt,
    PipelineToolStore,
    identity,
    match_checkpoint,
    request_id,
    tool_name,
)

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")
T = TypeVar("T")


@dataclass(frozen=True, kw_only=True)
class PipelineToolRequest(Generic[I]):
    request_id: str
    input: I | None = None


@dataclass(frozen=True, kw_only=True)
class PipelineToolStart:
    state: Literal["started", "start_unknown"]
    receipt: PipelineToolReceipt
    replayed: bool

    def to_dict(self) -> dict[str, Any]:
        return dict(state=self.state, receipt=self.receipt.to_dict(), replayed=self.replayed)


@dataclass(frozen=True, kw_only=True)
class PipelineToolApproval:
    kind: Literal["approval", "input"]
    id: str
    name: str
    expires_at: str
    review_url: str


@dataclass(frozen=True, kw_only=True)
class PipelineToolStatus:
    state: Literal[
        "start_unknown", "running", "awaiting_approval", "awaiting_input", "succeeded", "failed", "cancelled"
    ]
    receipt: PipelineToolReceipt
    steps: tuple[PipelineStepInspection, ...]
    approval: PipelineToolApproval | None

    def to_dict(self) -> dict[str, Any]:
        return dict(
            state=self.state,
            receipt=self.receipt.to_dict(),
            steps=[asdict(s) for s in self.steps],
            approval=asdict(self.approval) if self.approval else None,
        )


def review_url(value: str) -> str:
    try:
        u = urlsplit(value)
        if (
            not u.netloc
            or u.username
            or u.password
            or u.query
            or u.fragment
            or u.scheme != "https"
            and not (u.scheme == "http" and u.hostname in ("127.0.0.1", "localhost", "::1"))
        ):
            raise ValueError
        return urlunsplit((u.scheme, u.netloc, u.path or "/", "", ""))
    except Exception:
        raise PipelineToolError("invalid_input") from None


class PipelineTool(Generic[I, O, A]):
    def __init__(
        self,
        contract: JobContract[I, O],
        *,
        name: str,
        description: str,
        scope: PipelineScope,
        store: PipelineToolStore,
        token: str,
        authorize: Callable[[PipelineToolRequest[I], A, PipelineToolAction], Awaitable[PipelineToolActor | None]],
        gate_store: PipelineGateStore | None = None,
        review_base_url: str | None = None,
        httpx_args: dict[str, Any] | None = None,
    ):
        if (
            not tool_name(name)
            or not isinstance(description, str)
            or not description.strip()
            or len(description) > 4096
            or not token
            or bool(gate_store) != bool(review_base_url)
        ):
            raise PipelineToolError("invalid_input")
        self.name, self.description = name, description
        self.contract = JobContract[I, O](contract.spec)
        self._scope, self._store, self._token, self._authorize = event_pipeline_scope(scope), store, token, authorize
        self._gates = gate_store
        self._review_url = review_url(review_base_url) if review_base_url else None
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
            raise PipelineToolError("invalid_input")

    async def _execute(
        self, timeout: float, work: Callable[[Callable[[], None], Callable[[], float]], Awaitable[T]]
    ) -> T:
        async def run(check: Callable[[], None]) -> T:
            end = asyncio.get_running_loop().time() + timeout

            def left() -> float:
                check()
                return max(0.000001, end - asyncio.get_running_loop().time())

            result = await work(check, left)
            check()
            return result

        try:
            return await _scope(timeout, run)
        except PipelineToolError:
            raise
        except JobError as error:
            raise PipelineToolError("deadline" if error.code == "deadline" else "request_failed") from None
        except Exception:
            raise PipelineToolError("request_failed") from None

    async def _prepare(
        self, request: PipelineToolRequest[I], access: A, action: PipelineToolAction, check: Callable[[], None]
    ) -> tuple[PipelineToolActor, dict[str, Any]]:
        try:
            actor = copy.deepcopy(await self._authorize(copy.deepcopy(request), access, action))
            check()
        except Exception:
            check()
            raise PipelineToolError("unauthorized") from None
        if actor is None or not pipeline_name(actor.subject_id) or not pipeline_name(actor.owner_id):
            raise PipelineToolError("unauthorized")
        id = identity(self._scope, self.name, actor.subject_id, request.request_id)
        return actor, dict(
            version=1,
            scope=self._scope.to_dict(),
            name=self.name,
            request_id=request.request_id,
            subject_id=actor.subject_id,
            owner_id=actor.owner_id,
            contract_sha256=self.contract.sha256,
            identity=id,
            idempotency_key="pipeline-tool:" + id,
        )

    def _bound(self, binding: dict[str, Any], raw: PipelineToolReceipt) -> PipelineToolReceipt:
        receipt = PipelineToolReceipt.from_dict(raw.to_dict())
        if any(job_value_hash(v) != job_value_hash(receipt.intent[k]) for k, v in binding.items()):
            raise PipelineToolError("receipt_conflict")
        return receipt

    async def _stored(self, work: Callable[[], Awaitable[T]], check: Callable[[], None]) -> T:
        check()
        try:
            value = await work()
            check()
            return value
        except PipelineToolError:
            raise
        except Exception:
            check()
            raise PipelineToolError("store_failed") from None

    def _client(self, owner: str) -> JobPipelineClient[I, O]:
        return JobPipelineClient(
            base_url=self._scope.base_url,
            scope=self._scope,
            token=self._token,
            owner_id=owner,
            contract=self.contract,
            httpx_args=self._httpx_args,
        )

    @staticmethod
    def _start_result(receipt: PipelineToolReceipt, replayed: bool) -> PipelineToolStart:
        return PipelineToolStart(
            state="started" if receipt.checkpoint else "start_unknown", receipt=receipt, replayed=replayed
        )

    async def start(self, input: I, id: str, access: A, *, timeout: float) -> PipelineToolStart:
        captured = self.contract.input(input)
        request = PipelineToolRequest(request_id=request_id(id), input=captured)

        async def work(check: Callable[[], None], left: Callable[[], float]) -> PipelineToolStart:
            actor, binding = await self._prepare(request, access, "start", check)
            trigger = dict(version=1, kind="tool", identity=binding["identity"])
            intent = binding | dict(
                input_sha256=job_value_hash(
                    dict(
                        version=1,
                        scope=self._scope.to_dict(),
                        owner_id=actor.owner_id,
                        contract_sha256=self.contract.sha256,
                        input=captured,
                        trigger=trigger,
                    )
                )
            )
            created, raw = await self._stored(lambda: self._store.reserve(intent), check)
            receipt = self._bound(binding, raw)
            if type(created) is not bool or job_value_hash(receipt.intent) != job_value_hash(intent):
                raise PipelineToolError("receipt_conflict")
            if not created:
                return self._start_result(receipt, True)
            if receipt.checkpoint is not None:
                raise PipelineToolError("receipt_conflict")
            try:
                async with self._client(actor.owner_id) as client:
                    run = await client.start(
                        captured, timeout=left(), idempotency_key=intent["idempotency_key"], trigger=trigger
                    )
                    check()
                    checkpoint = run.checkpoint
            except Exception:
                check()
                return self._start_result(receipt, False)
            match_checkpoint(intent, checkpoint)
            completed = self._bound(
                binding, await self._stored(lambda: self._store.complete(intent, checkpoint), check)
            )
            if completed.checkpoint != checkpoint:
                raise PipelineToolError("receipt_conflict")
            return self._start_result(completed, False)

        return await self._execute(timeout, work)

    async def _operation(
        self, id: str, access: A, action: PipelineToolAction, timeout: float, run_id: str | None = None
    ) -> Any:
        request = PipelineToolRequest[I](request_id=request_id(id))

        async def work(check: Callable[[], None], left: Callable[[], float]) -> Any:
            actor, binding = await self._prepare(request, access, action, check)
            raw = await self._stored(lambda: self._store.load(binding["identity"]), check)
            if raw is None:
                if action == "inspect":
                    return None
                raise PipelineToolError("receipt_conflict")
            receipt = self._bound(binding, raw)
            async with self._client(actor.owner_id) as client:
                if action == "reconcile":
                    run = await client.resume_by_id(request_id(run_id), timeout=left())
                    check()
                    match_checkpoint(receipt.intent, run.checkpoint)
                    completed = self._bound(
                        binding, await self._stored(lambda: self._store.complete(receipt.intent, run.checkpoint), check)
                    )
                    if completed.checkpoint != run.checkpoint:
                        raise PipelineToolError("receipt_conflict")
                    return self._start_result(completed, True)
                if receipt.checkpoint is None:
                    if action != "inspect":
                        raise PipelineToolError("start_unknown")
                    return PipelineToolStatus(state="start_unknown", receipt=receipt, steps=(), approval=None)
                run = await client.resume_by_id(receipt.checkpoint.run_id, timeout=left())
                check()
                match_checkpoint(receipt.intent, run.checkpoint)
                info = await (run.cancel(timeout=left()) if action == "cancel" else run.inspect(timeout=left()))
                check()
                if action == "result":
                    if info.status != "succeeded" or info.cancelled:
                        raise PipelineToolError("not_terminal")
                    return await run.result(timeout=left())
                state = (
                    "cancelled"
                    if info.cancelled
                    else "succeeded"
                    if info.status == "succeeded"
                    else "failed"
                    if info.status in ("failed", "dead")
                    else "running"
                )
                approval = None
                gates = self._gates
                if state == "running" and gates and self._review_url:
                    rows = await self._stored(lambda: gates.list(run.checkpoint.run_id, actor.owner_id), check)
                    if len(rows) > 100:
                        raise PipelineToolError("limit")
                    for raw_gate in rows:
                        gate = PipelineGateRecord[Any, Any].from_dict(raw_gate.to_dict())
                        i = gate.intent
                        if (
                            i["workflow_run_id"] != run.checkpoint.run_id
                            or i["owner_id"] != actor.owner_id
                            or i["pipeline_sha256"] != self.contract.sha256
                            or i["input_sha256"] != run.checkpoint.input_sha256
                            or job_value_hash(i["scope"]) != job_value_hash(self._scope.to_dict())
                        ):
                            raise PipelineToolError("receipt_conflict")
                        if gate.decision is None:
                            resolved = await self._stored(lambda: gates.resolve(i["id"]), check)
                            if resolved is None:
                                raise PipelineToolError("receipt_conflict")
                            current = PipelineGateRecord[Any, Any].from_dict(resolved.to_dict())
                            if job_value_hash(current.intent) != job_value_hash(i):
                                raise PipelineToolError("receipt_conflict")
                            if current.decision is None:
                                if approval is not None:
                                    raise PipelineToolError("receipt_conflict")
                                u = urlsplit(self._review_url)
                                url = urlunsplit(
                                    (
                                        u.scheme,
                                        u.netloc,
                                        u.path,
                                        urlencode([("run", run.checkpoint.run_id), ("gate", i["id"])]),
                                        "",
                                    )
                                )
                                approval = PipelineToolApproval(
                                    kind=i["kind"],
                                    id=i["id"],
                                    name=i["name"],
                                    expires_at=current.expires_at,
                                    review_url=url,
                                )
                                state = "awaiting_approval" if i["kind"] == "approval" else "awaiting_input"
                return PipelineToolStatus(state=cast(Any, state), receipt=receipt, steps=info.steps, approval=approval)

        return await self._execute(timeout, work)

    async def inspect(self, id: str, access: A, *, timeout: float) -> PipelineToolStatus | None:
        return cast(PipelineToolStatus | None, await self._operation(id, access, "inspect", timeout))

    async def result(self, id: str, access: A, *, timeout: float) -> O:
        return cast(O, await self._operation(id, access, "result", timeout))

    async def cancel(self, id: str, access: A, *, timeout: float) -> PipelineToolStatus:
        return cast(PipelineToolStatus, await self._operation(id, access, "cancel", timeout))

    async def reconcile(self, id: str, run_id: str, access: A, *, timeout: float) -> PipelineToolStart:
        return cast(PipelineToolStart, await self._operation(id, access, "reconcile", timeout, run_id))
