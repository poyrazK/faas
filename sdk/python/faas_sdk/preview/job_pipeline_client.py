"""Isolated typed start/attach/inspect/result/cancel for native job pipelines."""

from __future__ import annotations

import json
from collections.abc import Awaitable, Callable
from dataclasses import asdict, dataclass
from typing import Any, Generic, TypeVar

from .._async_request import RequestOptions
from .._typed_jobs import JobProgress
from .._wrapper import FaaSClient
from ..job_runtime import JobContract, _scope, job_value_hash
from .live_jobs import LiveJobSnapshot
from .pipeline_state import (
    PipelineError,
    PipelineFailure,
    PipelineScope,
    _hash,
    _parse_pipeline_lineage,
    _parse_pipeline_trigger,
    _uuid,
    pipeline_failure,
    pipeline_name,
)

I = TypeVar("I")
O = TypeVar("O")
T = TypeVar("T")


@dataclass(frozen=True, kw_only=True)
class PipelineCheckpoint:
    version: int
    run_id: str
    scope: PipelineScope
    owner_id: str
    contract_sha256: str
    input_sha256: str

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)

    @classmethod
    def from_dict(cls, value: Any) -> PipelineCheckpoint:
        try:
            if (
                not isinstance(value, dict)
                or set(value) != {"version", "run_id", "scope", "owner_id", "contract_sha256", "input_sha256"}
                or type(value["version"]) is not int
                or value["version"] != 1
                or not _uuid(value["run_id"])
                or not pipeline_name(value["owner_id"])
                or not _hash(value["contract_sha256"])
                or not _hash(value["input_sha256"])
            ):
                raise ValueError
            return cls(**(value | {"scope": PipelineScope.from_dict(value["scope"])}))
        except (ValueError, KeyError, TypeError):
            raise PipelineError("receipt_conflict") from None


@dataclass(frozen=True, kw_only=True)
class PipelineStepInspection:
    name: str
    status: str
    attempt: int


@dataclass(frozen=True, kw_only=True)
class PipelineInspection:
    run_id: str
    status: str
    cancelled: bool
    steps: tuple[PipelineStepInspection, ...]
    failed_step: str | None = None
    failure: PipelineFailure | None = None


class TypedPipelineRun(Generic[O]):
    def __init__(self, client: JobPipelineClient[Any, O], checkpoint: PipelineCheckpoint):
        self._client, self.checkpoint = client, checkpoint

    async def inspect(self, *, timeout: float) -> PipelineInspection:
        async def work(request: Any) -> PipelineInspection:
            value = await self._client._read(self.checkpoint, request)
            return await self._inspect(value, request)

        return await self._client._operation(timeout, work)

    async def _inspect(self, value: dict[str, Any], request: Any) -> PipelineInspection:
        rows = await request("/v1/workflows/runs/" + self.checkpoint.run_id + "/steps")
        steps = rows.get("steps")
        if (
            not isinstance(steps, list)
            or len(steps) > 1000
            or any(
                not isinstance(s, dict)
                or not isinstance(s.get("step_name"), str)
                or s.get("status")
                not in ("pending", "running", "awaiting_event", "succeeded", "failed", "dead", "skipped")
                or type(s.get("attempt")) is not int
                or s["attempt"] < 0
                for s in steps
            )
        ):
            raise PipelineError("receipt_conflict")
        result = tuple(
            PipelineStepInspection(name=s["step_name"], status=s["status"], attempt=s["attempt"]) for s in steps
        )
        return PipelineInspection(
            run_id=self.checkpoint.run_id,
            status=value["status"],
            cancelled=bool(value.get("cancelled_at")),
            steps=result,
            failed_step=next((s.name for s in result if s.status in ("failed", "dead")), None),
            failure=next(
                (
                    f
                    for s in steps
                    if s.get("status") in ("failed", "dead") and (f := pipeline_failure(s.get("error"))) is not None
                ),
                None,
            )
            or pipeline_failure(value.get("last_error")),
        )

    async def result(self, *, timeout: float) -> O:
        async def work(request: Any) -> O:
            value = await self._client._read(self.checkpoint, request)
            if value["status"] != "succeeded" or value.get("cancelled_at"):
                raise PipelineError("invalid_result")
            output = value.get("output")
            checked = self._client._envelope(output, "output")
            return self._client.contract.output(checked["output"])

        return await self._client._operation(timeout, work)

    async def cancel(self, *, timeout: float) -> PipelineInspection:
        """Cancel orchestration; accepted Jobs continue independently."""

        async def work(request: Any) -> PipelineInspection:
            await self._client._read(self.checkpoint, request)
            value = self._client._validate(
                await request("/v1/workflows/runs/" + self.checkpoint.run_id + "/cancel", "POST"), self.checkpoint
            )
            return await self._inspect(value, request)

        return await self._client._operation(timeout, work)


class JobPipelineClient(Generic[I, O]):
    """Owns an isolated client. Authorize owner_id in your app before use."""

    def __init__(
        self,
        *,
        base_url: str,
        token: str,
        scope: PipelineScope,
        owner_id: str,
        contract: JobContract[I, O],
        **httpx_args: Any,
    ):
        if base_url != scope.base_url or not pipeline_name(owner_id):
            raise PipelineError("scope_mismatch")
        self.scope, self.owner_id, self.contract = scope, owner_id, contract
        self._client = FaaSClient(base_url=base_url, token=token, follow_redirects=False, **httpx_args)

    async def __aenter__(self) -> JobPipelineClient[I, O]:
        return self

    async def __aexit__(self, *_: Any) -> None:
        await self._client.aclose()

    async def _operation(self, timeout: float, work: Callable[[Any], Awaitable[T]]) -> T:
        async def run(check: Callable[[], None]) -> T:
            calls = 0

            async def request(
                path: str, method: str = "GET", body: Any = None, key: str | None = None
            ) -> dict[str, Any]:
                nonlocal calls
                check()
                calls += 1
                if calls > 8:
                    raise PipelineError("operation_limit")
                kwargs = {"json": body} if body is not None else {}
                headers = {"Idempotency-Key": key} if key is not None else {}

                async def send() -> dict[str, Any]:
                    async with self._client.async_httpx_client.stream(
                        method, path, headers=headers, **kwargs
                    ) as response:
                        response.raise_for_status()
                        data = bytearray()
                        async for chunk in response.aiter_bytes(chunk_size=8192):
                            check()
                            if len(data) + len(chunk) > 1024 * 1024:
                                raise PipelineError("journal_limit")
                            data.extend(chunk)
                    check()
                    value = json.loads(data)
                    if not isinstance(value, dict):
                        raise PipelineError("receipt_conflict")
                    return value

                return await self._client.arequest(send, options=RequestOptions(timeout=0, retry=False))

            try:
                value = await work(request)
                check()
                return value
            except PipelineError:
                raise
            except Exception:
                check()
                raise PipelineError("request_failed") from None

        return await _scope(timeout, run)

    async def _verify(self, request: Any) -> None:
        account = await request("/v1/account")
        if account.get("id") != self.scope.account_id:
            raise PipelineError("scope_mismatch")
        app = await request("/v1/apps/" + self.scope.app_slug)
        if app.get("id") != self.scope.app_id:
            raise PipelineError("scope_mismatch")

    def _envelope(self, value: Any, field: str = "input") -> dict[str, Any]:
        if (
            not isinstance(value, dict)
            or value.get("version") != 1
            or value.get("owner_id") != self.owner_id
            or value.get("contract_sha256") != self.contract.sha256
            or value.get("scope") != self.scope.to_dict()
            or field not in value
        ):
            raise PipelineError("scope_mismatch")
        content = self.contract.input(value[field]) if field == "input" else self.contract.output(value[field])
        return {
            "version": 1,
            "scope": self.scope.to_dict(),
            "owner_id": self.owner_id,
            "contract_sha256": self.contract.sha256,
            field: content,
            **({"lineage": _parse_pipeline_lineage(value["lineage"])} if "lineage" in value else {}),
            **({"trigger": _parse_pipeline_trigger(value["trigger"])} if "trigger" in value else {}),
        }

    def _checkpoint(self, run_id: str, value: Any) -> PipelineCheckpoint:
        if not _uuid(run_id):
            raise PipelineError("receipt_conflict")
        return PipelineCheckpoint(
            version=1,
            run_id=run_id,
            scope=self.scope,
            owner_id=self.owner_id,
            contract_sha256=self.contract.sha256,
            input_sha256=job_value_hash(self._envelope(value)),
        )

    def _validate(self, value: Any, checkpoint: PipelineCheckpoint | None = None) -> dict[str, Any]:
        if (
            not isinstance(value, dict)
            or not _uuid(value.get("id"))
            or value.get("app_id") != self.scope.app_id
            or value.get("workflow_name") != self.contract.spec["name"]
            or value.get("platform_tenant_id") is not None
            or value.get("status") not in ("pending", "running", "awaiting_event", "succeeded", "failed", "dead")
        ):
            raise PipelineError("receipt_conflict")
        current = self._checkpoint(value["id"], value.get("input"))
        if checkpoint is not None and current != checkpoint:
            raise PipelineError("receipt_conflict")
        return value

    async def _read(self, checkpoint: PipelineCheckpoint, request: Any) -> dict[str, Any]:
        await self._verify(request)
        return self._validate(await request("/v1/workflows/runs/" + checkpoint.run_id), checkpoint)

    def resume(self, checkpoint: PipelineCheckpoint) -> TypedPipelineRun[O]:
        checkpoint = PipelineCheckpoint.from_dict(checkpoint.to_dict())
        if (
            checkpoint.scope != self.scope
            or checkpoint.owner_id != self.owner_id
            or checkpoint.contract_sha256 != self.contract.sha256
        ):
            raise PipelineError("receipt_conflict")
        return TypedPipelineRun(self, checkpoint)

    async def resume_by_id(self, run_id: str, *, timeout: float) -> TypedPipelineRun[O]:
        if not _uuid(run_id):
            raise PipelineError("invalid_input")

        async def work(request: Any) -> TypedPipelineRun[O]:
            await self._verify(request)
            value = self._validate(await request("/v1/workflows/runs/" + run_id))
            if value["id"] != run_id:
                raise PipelineError("receipt_conflict")
            return self.resume(self._checkpoint(run_id, value["input"]))

        return await self._operation(timeout, work)

    async def start(
        self,
        value: I,
        *,
        timeout: float,
        idempotency_key: str,
        lineage: dict[str, Any] | None = None,
        trigger: dict[str, Any] | None = None,
    ) -> TypedPipelineRun[O]:
        import re

        captured = self.contract.input(value)
        bound_lineage = _parse_pipeline_lineage(lineage) if lineage is not None else None
        bound_trigger = _parse_pipeline_trigger(trigger) if trigger is not None else None
        if not isinstance(idempotency_key, str) or re.fullmatch(r"[!-~]{1,128}", idempotency_key) is None:
            raise PipelineError("invalid_input")

        async def work(request: Any) -> TypedPipelineRun[O]:
            await self._verify(request)
            envelope = {
                "version": 1,
                "contract_sha256": self.contract.sha256,
                "scope": self.scope.to_dict(),
                "owner_id": self.owner_id,
                "input": captured,
                **({"lineage": bound_lineage} if bound_lineage is not None else {}),
                **({"trigger": bound_trigger} if bound_trigger is not None else {}),
            }
            try:
                accepted = await request(
                    "/v1/apps/" + self.scope.app_slug + "/workflows/" + self.contract.spec["name"] + "/runs",
                    "POST",
                    envelope,
                    idempotency_key,
                )
            except Exception:
                raise PipelineError("acceptance_unknown") from None
            value = self._validate(accepted)
            if job_value_hash(self._envelope(value["input"])) != job_value_hash(envelope):
                raise PipelineError("receipt_conflict")
            return self.resume(self._checkpoint(value["id"], value["input"]))

        return await self._operation(timeout, work)


async def job_pipeline_live_snapshot(
    run: TypedPipelineRun[O],
    *,
    resource_id: str,
    steps: list[str],
    timeout: float,
    inspect_job: Callable[[str], Awaitable[JobProgress | None]],
    report_ready: Callable[[O], Awaitable[bool]] | None = None,
) -> LiveJobSnapshot:
    """Authorize the workflow owner first; verify reports before returning ready."""

    async def work(check: Callable[[], None]) -> LiveJobSnapshot:
        workflow = await run.inspect(timeout=timeout)
        check()
        states = []
        for step in steps:
            states.append(await inspect_job(step))
            check()
        status = (
            "cancelled"
            if workflow.cancelled
            else "dead_letter"
            if workflow.status == "dead"
            else workflow.status
            if workflow.status in ("failed", "succeeded")
            else "queued"
            if workflow.status == "pending"
            else "running"
        )
        report = "none"
        if status == "succeeded" and report_ready is not None:
            output = await run.result(timeout=timeout)
            check()
            report = "ready" if await report_ready(output) else "pending"
            check()

        def count(value: str) -> int:
            return sum(s is not None and s.status == value for s in states)

        return LiveJobSnapshot.from_dict(
            dict(
                version=1,
                resource_id=resource_id,
                run_id=run.checkpoint.run_id,
                status=status,
                total=len(states),
                succeeded=count("succeeded"),
                failed=count("failed") + count("dead_letter"),
                cancelled=count("cancelled"),
                running=count("running"),
                report=report,
                failures=[
                    dict(input_id=step, code="job_failed")
                    for step, state in zip(steps, states, strict=True)
                    if state is not None and state.status in ("failed", "dead_letter")
                ],
            ),
            resource_id,
        )

    return await _scope(timeout, work)
