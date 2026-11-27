"""Typed task handles composing immutable inputs, receipts and durable outputs."""

from __future__ import annotations

import asyncio
import hashlib
import inspect
import json
import time
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Any, Generic, TypeVar
from urllib.parse import quote

import httpx

from .executions import prepare_execution_file, upload_execution_file
from .models.execution_response import ExecutionResponse
from .models.run_artifact_delivery import RunArtifactDelivery
from .types import Unset

Input = TypeVar("Input")
Output = TypeVar("Output")


@dataclass(frozen=True)
class TaskIntent:
    idempotency_key: str
    request: dict[str, Any]


@dataclass(frozen=True)
class TaskDestination:
    app: str
    route: str
    artifacts: Mapping[str, str]


@dataclass(frozen=True)
class TaskResult(Generic[Output]):
    result: Output
    receipt: ExecutionResponse
    deliveries: dict[str, RunArtifactDelivery]


class TaskExecutionError(RuntimeError):
    def __init__(self, receipt: ExecutionResponse):
        status = (
            "invalid result contract"
            if getattr(receipt.result_validation, "status", None) == "invalid"
            else receipt.status
        )
        super().__init__(f"Task {receipt.id}: {status}")
        self.receipt = receipt


class TaskDeliveryError(RuntimeError):
    def __init__(self, receipt: ExecutionResponse, delivery: RunArtifactDelivery):
        super().__init__(f"Artifact delivery {delivery.id}: {delivery.status}")
        self.receipt, self.delivery = receipt, delivery


class TaskSubmissionError(RuntimeError):
    def __init__(self, intent: TaskIntent):
        super().__init__("Task submission was not confirmed; retry the same intent and key")
        self.intent = intent


def _success(receipt: ExecutionResponse) -> None:
    if receipt.status != "succeeded" or getattr(receipt.result_validation, "status", None) == "invalid":
        raise TaskExecutionError(receipt)


def _settings(timeout: float, poll_interval: float) -> float:
    import math

    if not math.isfinite(timeout) or timeout <= 0 or not math.isfinite(poll_interval) or poll_interval <= 0:
        raise ValueError("Invalid task wait limits")
    return time.monotonic() + timeout


def _remaining(deadline: float) -> float:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise TimeoutError("Task wait expired; resume the original Run")
    return remaining


def _files(files: Mapping[str, Any]) -> None:
    if len(files) > 8 or any(
        not p
        or len(p.encode()) > 256
        or "\\" in p
        or "\x00" in p
        or any(part in ("", ".", "..") for part in p.split("/"))
        for p in files
    ):
        raise ValueError("Invalid task input file paths or count")


def _missing_files(response: httpx.Response) -> bool:
    return response.status_code == 409 and response.json().get("code") == "execution_files_not_ready"


class Task(Generic[Input, Output]):
    """A pinned task version, with an explicit runtime result decoder.

    Input/result schemas are enforced by the published server contract. Sources
    are seekable binary streams owned by the caller throughout submission.
    """

    def __init__(self, client: Any, name: str, version: int, decode_result: Callable[[Any], Output]):
        if not name or isinstance(version, bool) or not isinstance(version, int) or version < 1:
            raise ValueError("An exact task name and positive version are required")
        self.client, self.name, self.version, self.decode_result = client, name, version, decode_result

    def _body(
        self, value: Input, idempotency_key: str, files: Mapping[str, Any], customer_ref: str | None
    ) -> dict[str, Any]:
        if (
            not idempotency_key
            or idempotency_key.strip() != idempotency_key
            or len(idempotency_key.encode()) > 128
            or any(ord(c) < 32 or ord(c) == 127 for c in idempotency_key)
        ):
            raise ValueError("A bounded explicit task idempotency key is required")
        _files(files)
        body = {"name": self.name, "version": self.version, "input": value}
        if customer_ref is not None:
            body["customer_ref"] = customer_ref
        return json.loads(json.dumps(body, allow_nan=False))

    def submit(self, intent: TaskIntent) -> TaskRun[Output]:
        """Replay a saved wire intent without reopening the original files."""
        if (
            intent.request.get("name") != self.name
            or intent.request.get("version") != self.version
            or not intent.idempotency_key
        ):
            raise ValueError("Intent does not belong to this task version")
        try:
            response = self.client.httpx_client.post(
                "/v1/run-tasks/invoke", json=intent.request, headers={"Idempotency-Key": intent.idempotency_key}
            )
            response.raise_for_status()
            return self.attach(ExecutionResponse.from_dict(response.json()))
        except Exception as cause:
            raise TaskSubmissionError(intent) from cause

    async def asubmit(self, intent: TaskIntent) -> TaskRun[Output]:
        if (
            intent.request.get("name") != self.name
            or intent.request.get("version") != self.version
            or not intent.idempotency_key
        ):
            raise ValueError("Intent does not belong to this task version")
        try:
            response = await self.client.async_httpx_client.post(
                "/v1/run-tasks/invoke", json=intent.request, headers={"Idempotency-Key": intent.idempotency_key}
            )
            response.raise_for_status()
            return self.attach(ExecutionResponse.from_dict(response.json()))
        except Exception as cause:
            raise TaskSubmissionError(intent) from cause

    def attach(self, receipt: ExecutionResponse) -> TaskRun[Output]:
        if getattr(receipt.task, "name", None) != self.name or getattr(receipt.task, "version", None) != self.version:
            raise ValueError("Run receipt does not belong to this task version")
        return TaskRun(self.client, receipt, self.decode_result)

    def start(
        self,
        value: Input,
        *,
        idempotency_key: str,
        files: Mapping[str, Any] | None = None,
        customer_ref: str | None = None,
        on_prepared: Callable[[TaskIntent], None] | None = None,
    ) -> TaskRun[Output]:
        sources = files or {}
        body = self._body(value, idempotency_key, sources, customer_ref)
        prepared = [(path, prepare_execution_file(self.client, source), source) for path, source in sources.items()]
        if prepared:
            body["input_files"] = [{"path": path, "file_id": str(meta.id)} for path, meta, _ in prepared]
        intent = TaskIntent(idempotency_key, body)
        if on_prepared:
            on_prepared(TaskIntent(idempotency_key, json.loads(json.dumps(body))))

        def invoke() -> httpx.Response:
            return self.client.httpx_client.post(
                "/v1/run-tasks/invoke", json=body, headers={"Idempotency-Key": idempotency_key}
            )

        try:
            response = invoke()
            if prepared and _missing_files(response):
                for _, meta, source in prepared:
                    if meta.upload_required:
                        position = source.tell()
                        try:
                            upload_execution_file(self.client, meta, source)
                        finally:
                            source.seek(position)
                response = invoke()
            response.raise_for_status()
            receipt = ExecutionResponse.from_dict(response.json())
            return self.attach(receipt)
        except Exception as cause:
            raise TaskSubmissionError(intent) from cause

    async def astart(
        self,
        value: Input,
        *,
        idempotency_key: str,
        files: Mapping[str, Any] | None = None,
        customer_ref: str | None = None,
        on_prepared: Callable[[TaskIntent], Any] | None = None,
    ) -> TaskRun[Output]:
        sources = files or {}
        body = self._body(value, idempotency_key, sources, customer_ref)
        prepared = []
        from .models.execution_file_response import ExecutionFileResponse

        for path, source in sources.items():

            def fingerprint(source: Any = source) -> dict[str, Any]:
                position, size, digest = source.tell(), 0, hashlib.sha256()
                try:
                    while chunk := source.read(65536):
                        size += len(chunk)
                        if size > 64 * 1024 * 1024:
                            raise ValueError("Run file exceeds the 64 MiB hard limit")
                        digest.update(chunk)
                finally:
                    source.seek(position)
                return {"size_bytes": size, "sha256": "sha256:" + digest.hexdigest()}

            metadata = await asyncio.to_thread(fingerprint)
            response = await self.client.async_httpx_client.post("/v1/execution-files", json=metadata)
            response.raise_for_status()
            prepared.append((path, ExecutionFileResponse.from_dict(response.json()), source))
        if prepared:
            body["input_files"] = [{"path": path, "file_id": str(meta.id)} for path, meta, _ in prepared]
        intent = TaskIntent(idempotency_key, body)
        if on_prepared:
            outcome = on_prepared(TaskIntent(idempotency_key, json.loads(json.dumps(body))))
            if inspect.isawaitable(outcome):
                await outcome

        async def invoke() -> httpx.Response:
            return await self.client.async_httpx_client.post(
                "/v1/run-tasks/invoke", json=body, headers={"Idempotency-Key": idempotency_key}
            )

        try:
            response = await invoke()
            if prepared and _missing_files(response):
                for _, meta, source in prepared:
                    if not meta.upload_required:
                        continue
                    position = source.tell()

                    async def chunks(source: Any = source, size: int = meta.size_bytes):
                        remaining = size
                        while remaining:
                            chunk = await asyncio.to_thread(source.read, min(65536, remaining))
                            if not chunk:
                                raise ValueError("Run file source ended before its declared size")
                            remaining -= len(chunk)
                            yield chunk

                    try:
                        upload = await self.client.async_httpx_client.put(
                            f"/v1/execution-files/{quote(str(meta.id), safe='')}/content",
                            content=chunks(),
                            headers={
                                "Content-Type": "application/octet-stream",
                                "Content-Length": str(meta.size_bytes),
                                "X-Content-SHA256": str(meta.sha256),
                            },
                            timeout=120.0,
                        )
                        upload.raise_for_status()
                    finally:
                        source.seek(position)
                response = await invoke()
            response.raise_for_status()
            receipt = ExecutionResponse.from_dict(response.json())
            return self.attach(receipt)
        except Exception as cause:
            raise TaskSubmissionError(intent) from cause

    def run(
        self,
        value: Input,
        *,
        timeout: float = 360,
        poll_interval: float = 1,
        deliver: TaskDestination | None = None,
        **options: Any,
    ) -> TaskResult[Output]:
        _settings(timeout, poll_interval)
        run = self.start(value, **options)
        result = run.result(timeout=timeout, poll_interval=poll_interval)
        deliveries = run.deliver(deliver, timeout=timeout, poll_interval=poll_interval) if deliver else {}
        return TaskResult(result, run.receipt, deliveries)

    async def arun(
        self,
        value: Input,
        *,
        timeout: float = 360,
        poll_interval: float = 1,
        deliver: TaskDestination | None = None,
        **options: Any,
    ) -> TaskResult[Output]:
        _settings(timeout, poll_interval)
        run = await self.astart(value, **options)
        result = await run.aresult(timeout=timeout, poll_interval=poll_interval)
        deliveries = await run.adeliver(deliver, timeout=timeout, poll_interval=poll_interval) if deliver else {}
        return TaskResult(result, run.receipt, deliveries)


class TaskRun(Generic[Output]):
    def __init__(self, client: Any, receipt: ExecutionResponse, decode_result: Callable[[Any], Output]):
        self.client, self.receipt, self.decode_result = client, receipt, decode_result

    @property
    def id(self):
        return self.receipt.id

    def _update(self, response: httpx.Response) -> None:
        response.raise_for_status()
        receipt = ExecutionResponse.from_dict(response.json())
        if receipt.id != self.id or getattr(receipt.task, "id", None) != getattr(self.receipt.task, "id", None):
            raise ValueError("Task receipt identity changed")
        self.receipt = receipt

    def result(self, *, timeout: float = 360, poll_interval: float = 1) -> Output:
        deadline = _settings(timeout, poll_interval)
        while self.receipt.status in ("queued", "restoring", "running"):
            self._update(self.client.httpx_client.get(f"/v1/executions/{self.id}", timeout=_remaining(deadline)))
            if self.receipt.status in ("queued", "restoring", "running"):
                time.sleep(min(poll_interval, _remaining(deadline)))
        _success(self.receipt)
        return self.decode_result(self.receipt.result)

    async def aresult(self, *, timeout: float = 360, poll_interval: float = 1) -> Output:
        deadline = _settings(timeout, poll_interval)
        while self.receipt.status in ("queued", "restoring", "running"):
            response = await self.client.async_httpx_client.get(
                f"/v1/executions/{self.id}", timeout=_remaining(deadline)
            )
            self._update(response)
            if self.receipt.status in ("queued", "restoring", "running"):
                await asyncio.sleep(min(poll_interval, _remaining(deadline)))
        _success(self.receipt)
        return self.decode_result(self.receipt.result)

    def cancel(self) -> None:
        self.client.httpx_client.delete(f"/v1/executions/{self.id}").raise_for_status()

    async def acancel(self) -> None:
        (await self.client.async_httpx_client.delete(f"/v1/executions/{self.id}")).raise_for_status()

    def _artifact(self, name: str, content_type: str) -> dict[str, Any]:
        _success(self.receipt)
        artifacts = [] if isinstance(self.receipt.artifacts, Unset) else self.receipt.artifacts
        artifact = next((a for a in artifacts if a.name == name), None)
        if artifact is None:
            raise ValueError(f"Task has no declared artifact {name}")
        return {
            "idempotency_key": f"task:{self.id}:{hashlib.sha256(name.encode()).hexdigest()}",
            "execution_id": str(self.id),
            "artifact_name": name,
            "size_bytes": artifact.size_bytes,
            "sha256": artifact.sha256,
            "content_type": content_type,
        }

    def deliver(
        self, destination: TaskDestination, *, timeout: float = 360, poll_interval: float = 1
    ) -> dict[str, RunArtifactDelivery]:
        deadline, outputs = _settings(timeout, poll_interval), {}
        path = f"/v1/apps/{quote(destination.app, safe='')}/run-artifact-deliveries/{quote(destination.route, safe='')}"
        for name, content_type in destination.artifacts.items():
            response = self.client.httpx_client.post(
                path, json=self._artifact(name, content_type), timeout=_remaining(deadline)
            )
            response.raise_for_status()
            delivery = RunArtifactDelivery.from_dict(response.json())
            try:
                while delivery.status == "pending":
                    if time.monotonic() >= deadline:
                        raise TaskDeliveryError(self.receipt, delivery)
                    time.sleep(min(poll_interval, _remaining(deadline)))
                    response = self.client.httpx_client.get(f"{path}/{delivery.id}", timeout=_remaining(deadline))
                    response.raise_for_status()
                    next_delivery = RunArtifactDelivery.from_dict(response.json())
                    if next_delivery.id != delivery.id:
                        raise ValueError("Delivery receipt identity changed")
                    delivery = next_delivery
            except (TimeoutError, httpx.TimeoutException) as cause:
                raise TaskDeliveryError(self.receipt, delivery) from cause
            if delivery.status != "completed":
                raise TaskDeliveryError(self.receipt, delivery)
            outputs[name] = delivery
        return outputs

    async def adeliver(
        self, destination: TaskDestination, *, timeout: float = 360, poll_interval: float = 1
    ) -> dict[str, RunArtifactDelivery]:
        deadline, outputs = _settings(timeout, poll_interval), {}
        path = f"/v1/apps/{quote(destination.app, safe='')}/run-artifact-deliveries/{quote(destination.route, safe='')}"
        for name, content_type in destination.artifacts.items():
            response = await self.client.async_httpx_client.post(
                path, json=self._artifact(name, content_type), timeout=_remaining(deadline)
            )
            response.raise_for_status()
            delivery = RunArtifactDelivery.from_dict(response.json())
            try:
                while delivery.status == "pending":
                    if time.monotonic() >= deadline:
                        raise TaskDeliveryError(self.receipt, delivery)
                    await asyncio.sleep(min(poll_interval, _remaining(deadline)))
                    response = await self.client.async_httpx_client.get(
                        f"{path}/{delivery.id}", timeout=_remaining(deadline)
                    )
                    response.raise_for_status()
                    next_delivery = RunArtifactDelivery.from_dict(response.json())
                    if next_delivery.id != delivery.id:
                        raise ValueError("Delivery receipt identity changed")
                    delivery = next_delivery
            except (TimeoutError, httpx.TimeoutException) as cause:
                raise TaskDeliveryError(self.receipt, delivery) from cause
            if delivery.status != "completed":
                raise TaskDeliveryError(self.receipt, delivery)
            outputs[name] = delivery
        return outputs
