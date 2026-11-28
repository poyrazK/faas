"""Typed consumer for a pipeline tool published through MCP."""

from __future__ import annotations

import asyncio
import copy
import json
import math
import re
from collections.abc import AsyncGenerator, AsyncIterator, Awaitable, Callable
from dataclasses import asdict, dataclass
from datetime import datetime
from typing import Any, Generic, Literal, Protocol, TypedDict, TypeVar, cast

from ..job_runtime import JobContract, JobError, _scope, job_value_hash
from ._pipeline_tool_protocol import WIRE
from .live_work_inbox import (
    LiveWorkInboxError,
    LiveWorkInboxSink,
    LiveWorkInboxSource,
    _check_review,
    _watch_inbox_snapshots,
)
from .pipeline_start_preview import _TOKEN_PATTERN, PipelineStartPreview
from .pipeline_state import _hash, _is_pinned_source_failure, _uuid
from .pipeline_task_approvals import PipelineTaskApprovalChoice, PipelineTaskApprovalReceipt, PipelineTaskApprovalView
from .pipeline_task_handoffs import (
    _CURSOR,
    _PAGE_LIMIT,
    PipelineTaskHandoffPage,
    PipelineTaskHandoffSnapshot,
    PipelineTaskHandoffSummary,
    PipelineTaskHandoffView,
)
from .pipeline_task_input import PipelineTaskFormRequest
from .pipeline_task_manifest import (
    PIPELINE_TASK_MANIFEST_MAX_BYTES,
    PIPELINE_TASK_MANIFEST_URI,
    PipelineTaskManifest,
    parse_pipeline_task_manifest,
)
from .pipeline_task_recovery import PipelineTaskRecoveryPreview
from .pipeline_tasks import PipelineTaskProgress
from .work_inbox import WorkInboxEntry

I = TypeVar("I")
O = TypeVar("O")
R = TypeVar("R")
T = TypeVar("T")
P = TypeVar("P")
PipelineTaskClientCode = Literal[
    "invalid_input",
    "invalid_response",
    "not_found",
    "unauthorized",
    "unsupported",
    "request_failed",
    "deadline",
    "cancelled",
    "tool_failed",
    "revision_unavailable",
    "preview_required",
    "preview_stale",
    "recovery_blocked",
    "recovery_exists",
]
PipelineTaskClientMethod = Literal["tools/call", "tasks/get", "tasks/update", "resources/read"]
_NAME = r"[a-z][a-z0-9_]{0,47}"
_MIME = r"[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+"
_URI = rf"gregale-pipeline://[a-f0-9]{{64}}/({_NAME})/[A-Za-z0-9_-]{{64}}"
_KEY = r"[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{22}"
_EOF = object()


class PipelineTaskClientError(Exception):
    def __init__(self, code: PipelineTaskClientCode):
        super().__init__("Pipeline Task client: " + code)
        self.code = code


class PipelineTaskTransport(Protocol):
    async def request(
        self,
        method: PipelineTaskClientMethod,
        params: dict[str, Any],
        *,
        timeout: float,
    ) -> Any: ...


class PipelineTaskCheckpoint(TypedDict):
    version: Literal[1]
    tool: str
    contract_sha256: str
    request_id: str
    task_id: str


class PipelineTaskResource(TypedDict):
    uri: str
    mimeType: str
    text: str


class PipelineTaskArtifactLink(TypedDict):
    type: Literal["resource_link"]
    uri: str
    name: str
    description: str
    mimeType: str


@dataclass(frozen=True, slots=True)
class _Observation:
    checkpoint: PipelineTaskCheckpoint
    progress: PipelineTaskProgress
    status_message: str
    poll_interval_ms: int


@dataclass(frozen=True, slots=True)
class PipelineTaskStatusObservation(_Observation):
    status: Literal["working", "failed", "cancelled"]


@dataclass(frozen=True, slots=True)
class PipelineTaskClarification(_Observation):
    forms: dict[str, PipelineTaskFormRequest]
    status: Literal["input_required"] = "input_required"


@dataclass(frozen=True, slots=True)
class PipelineTaskCompletion(_Observation, Generic[O]):
    output: O
    artifacts: tuple[PipelineTaskArtifactLink, ...]
    status: Literal["completed"] = "completed"


PipelineTaskObservation = PipelineTaskStatusObservation | PipelineTaskClarification | PipelineTaskCompletion[O]


def _valid_timeout(timeout: float) -> bool:
    return type(timeout) in (int, float) and math.isfinite(timeout) and 0 < timeout <= 2147483.647


async def _bounded(timeout: float, work: Callable[[], Awaitable[T]]) -> T:
    if not _valid_timeout(timeout):
        raise PipelineTaskClientError("invalid_input")

    async def run(check: Callable[[], None]) -> T:
        value = await work()
        check()
        return value

    try:
        return await _scope(timeout, run)
    except PipelineTaskClientError:
        raise
    except JobError as error:
        raise PipelineTaskClientError("deadline" if error.code == "deadline" else "request_failed") from None
    except Exception:
        raise PipelineTaskClientError("request_failed") from None


def _record(value: Any) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise PipelineTaskClientError("invalid_response")
    return value


def _check_tool_result(result: dict[str, Any], *, handoff: bool = False) -> None:
    if result.get("isError") is not True:
        return
    content = result.get("content")
    first = content[0] if isinstance(content, list) and content else None
    if isinstance(first, dict) and first.get("type") == "text":
        text = first.get("text")
        if isinstance(text, str) and len(text) <= 256:
            try:
                error = json.loads(text)
            except ValueError:
                error = None
            if isinstance(error, dict):
                code = error.get("code")
                if code in ("revision_unavailable", "preview_required", "preview_stale",
                            "recovery_blocked", "recovery_exists", "not_found") or (handoff and code in ("invalid_input", "deadline")):
                    raise PipelineTaskClientError(code)
    raise PipelineTaskClientError("tool_failed")


def _read_preview(result: dict[str, Any], contract: JobContract[None, P]) -> PipelineStartPreview[P]:
    _check_tool_result(result)
    body = _record(result.get("structuredContent"))
    token, expires = body.get("preview_token"), body.get("expires_at")
    if (
        result.get("isError") is not False or not isinstance(token, str) or not re.fullmatch(_TOKEN_PATTERN, token)
        or type(expires) is not int or expires != int(token.split(".", 1)[0])
    ):
        raise PipelineTaskClientError("invalid_response")
    return PipelineStartPreview(
        summary=_output(contract, body.get("summary"), "invalid_response"), preview_token=token, expires_at=expires,
    )


def _output(contract: JobContract[I, O], value: Any, code: Literal["invalid_response", "invalid_input"]) -> O:
    try:
        return contract.output(value)
    except (JobError, ValueError, TypeError):
        raise PipelineTaskClientError(code) from None


def _size(value: Any) -> int:
    return len(json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode())


def _form_contract(value: Any) -> JobContract[None, Any]:
    params = _record(value)
    message = params.get("message")
    if (
        params.get("mode") != "form"
        or not isinstance(message, str)
        or not message.strip()
        or len(message.encode("utf-16-le", errors="surrogatepass")) > 2048
    ):
        raise PipelineTaskClientError("invalid_response")
    try:
        schema = _record(params.get("requestedSchema"))
        if (
            schema.get("type") != "object"
            or not schema.get("properties")
            or any(
                field.get("type") not in ("string", "number", "integer", "boolean")
                for field in schema["properties"].values()
            )
            or _size(schema) > 16384
        ):
            raise ValueError
        return JobContract(dict(version=1, name="pipeline-task-form", input={"type": "null"}, output=schema))
    except Exception:
        raise PipelineTaskClientError("invalid_response") from None


def _link(value: Any) -> PipelineTaskArtifactLink:
    raw = _record(value)
    name, description, mime, uri = (raw.get(key) for key in ("name", "description", "mimeType", "uri"))
    route = re.fullmatch(_URI, uri) if isinstance(uri, str) else None
    if (
        raw.get("type") != "resource_link"
        or not isinstance(uri, str)
        or route is None
        or route[1] != name
        or not isinstance(description, str)
        or len(description.encode("utf-16-le", errors="surrogatepass")) > 1024
        or not isinstance(mime, str)
        or len(mime) > 128
        or not re.fullmatch(_MIME, mime)
    ):
        raise PipelineTaskClientError("invalid_response")
    return PipelineTaskArtifactLink(
        type="resource_link", uri=uri, name=route[1], description=description, mimeType=mime
    )


def _observe(raw: Any, checkpoint: PipelineTaskCheckpoint, contract: JobContract[I, O]) -> PipelineTaskObservation[O]:
    wire = _record(raw)
    message, interval, status = (wire.get(key) for key in ("statusMessage", "pollIntervalMs", "status"))
    if (
        wire.get("taskId") != checkpoint["task_id"]
        or not isinstance(message, str)
        or len(message.encode("utf-16-le", errors="surrogatepass")) > 32768
        or type(interval) is not int
        or not 1 <= interval <= 60000
    ):
        raise PipelineTaskClientError("invalid_response")
    meta = _record(_record(wire.get("_meta")).get("io.gregale/pipeline"))
    request_id, run_id, state, steps = (meta.get(key) for key in ("request_id", "run_id", "state", "steps"))
    if (
        request_id != checkpoint["request_id"]
        or "run_id" not in meta
        or (run_id is not None if state == "start_unknown" else not _uuid(run_id))
        or not isinstance(steps, list)
        or len(steps) > 1000
    ):
        raise PipelineTaskClientError("invalid_response")
    for step in steps:
        item = _record(step)
        name, step_status, attempt = (item.get(key) for key in ("name", "status", "attempt"))
        if (
            not isinstance(name, str)
            or len(name) > 128
            or not isinstance(step_status, str)
            or len(step_status) > 32
            or type(attempt) is not int
            or not 0 <= attempt <= 9007199254740991
        ):
            raise PipelineTaskClientError("invalid_response")
    progress = cast(
        PipelineTaskProgress, dict(request_id=request_id, run_id=run_id, state=state, steps=copy.deepcopy(steps))
    )
    if "replaces_task_id" in meta:
        replaces = meta["replaces_task_id"]
        if not _uuid(replaces) or replaces == checkpoint["task_id"]:
            raise PipelineTaskClientError("invalid_response")
        progress["replaces_task_id"] = replaces
    if "source_failure" in meta:
        failure = meta["source_failure"]
        if state != "failed" or not _is_pinned_source_failure(failure):
            raise PipelineTaskClientError("invalid_response")
        progress["source_failure"] = failure
    if state in ("awaiting_input", "awaiting_approval"):
        review = _record(meta.get("review"))
        if not _uuid(run_id) or set(review) != {"expires_at", "id", "kind", "review_url"}:
            raise PipelineTaskClientError("invalid_response")
        try:
            _check_review(cast(WorkInboxEntry, {"state": state, "run_id": run_id, "review": review}))
        except (ValueError, TypeError, AttributeError):
            raise PipelineTaskClientError("invalid_response") from None
        progress["review"] = cast(Any, copy.deepcopy(review))
    if state == "start_unknown" and "follow_up" in meta:
        follow = _record(meta["follow_up"])
        args = _record(follow.get("arguments"))
        if (
            follow.get("kind") != "tool_call"
            or follow.get("state") != state
            or follow.get("name") != checkpoint["tool"] + "_reconcile"
            or args.get("request_id") != request_id
            or not _uuid(args.get("run_id"))
        ):
            raise PipelineTaskClientError("invalid_response")
        progress["follow_up"] = {
            "kind": "tool_call",
            "state": "start_unknown",
            "name": follow["name"],
            "arguments": {"request_id": request_id, "run_id": args["run_id"]},
        }
    base = (copy.copy(checkpoint), progress, message, interval)
    if (
        status == "working"
        and state in ("start_unknown", "running", "awaiting_input", "awaiting_approval")
        or status == "failed"
        and state == "failed"
        or status == "cancelled"
        and state == "cancelled"
    ):
        return PipelineTaskStatusObservation(*base, status)
    if status == "input_required" and state == "awaiting_input":
        forms = _record(wire.get("inputRequests"))
        if len(forms) != 1:
            raise PipelineTaskClientError("invalid_response")
        key, raw_form = next(iter(forms.items()))
        form = _record(raw_form)
        if not isinstance(key, str) or not re.fullmatch(_KEY, key) or form.get("method") != "elicitation/create":
            raise PipelineTaskClientError("invalid_response")
        _form_contract(form.get("params"))
        return PipelineTaskClarification(*base, copy.deepcopy(forms))
    if status == "completed" and state == "succeeded" and run_id is not None:
        result = _record(wire.get("result"))
        structured = _record(result.get("structuredContent"))
        receipt = _record(structured.get("receipt"))
        content = result.get("content")
        if (
            result.get("isError") is not False
            or structured.get("state") != "succeeded"
            or receipt.get("tool") != checkpoint["tool"]
            or receipt.get("request_id") != request_id
            or receipt.get("run_id") != run_id
            or not _hash(receipt.get("identity"))
            or not isinstance(content, list)
            or len(content) > 100
            or "output" not in structured
        ):
            raise PipelineTaskClientError("invalid_response")
        artifacts = tuple(
            _link(block)
            for block in map(_record, content)
            if block.get("type") == "resource_link"
            and isinstance(block.get("uri"), str)
            and block["uri"].startswith("gregale-pipeline://")
        )
        if len({artifact["name"] for artifact in artifacts}) != len(artifacts):
            raise PipelineTaskClientError("invalid_response")
        return PipelineTaskCompletion(*base, _output(contract, structured["output"], "invalid_response"), artifacts)
    raise PipelineTaskClientError("invalid_response")


class PipelineTaskReader(Generic[I, O, R]):
    def __init__(self, client: PipelineTaskClient[I, O, R], checkpoint: PipelineTaskCheckpoint):
        self._client = client
        self._checkpoint = copy.copy(checkpoint)

    @property
    def checkpoint(self) -> PipelineTaskCheckpoint:
        return copy.copy(self._checkpoint)

    async def get(self, *, timeout: float) -> PipelineTaskObservation[O]:
        async def work() -> PipelineTaskObservation[O]:
            raw = await self._client.transport.request(
                "tasks/get", {"taskId": self._checkpoint["task_id"]}, timeout=timeout
            )
            if _record(raw).get("resultType") != "complete":
                raise PipelineTaskClientError("invalid_response")
            return _observe(raw, self._checkpoint, self._client.contract)

        return await _bounded(timeout, work)

    def _recovery_arguments(self, recovery_request_id: str) -> dict[str, str]:
        if not _uuid(recovery_request_id):
            raise PipelineTaskClientError("invalid_input")
        return {
            "task_id": self._checkpoint["task_id"],
            "recovery_request_id": recovery_request_id,
            "expected_contract_sha256": self._client.contract.sha256,
        }

    async def preview_recovery(
        self, recovery_request_id: str, contract: JobContract[None, P], *, timeout: float,
    ) -> PipelineTaskRecoveryPreview[P]:
        args = self._recovery_arguments(recovery_request_id)

        async def work() -> PipelineTaskRecoveryPreview[P]:
            raw = await self._client.transport.request("tools/call", {
                "name": WIRE["task_recovery"]["preview"]["name"], "arguments": args,
            }, timeout=timeout)
            result = _record(raw)
            preview = _read_preview(result, contract)
            body = _record(result.get("structuredContent"))
            replaces, previous = body.get("replaces_task_id"), body.get("previous_source_sha256")
            if replaces != self._checkpoint["task_id"] or not isinstance(previous, str) or not _hash(previous):
                raise PipelineTaskClientError("invalid_response")
            return PipelineTaskRecoveryPreview(
                **preview, replaces_task_id=replaces, previous_source_sha256=previous,
            )

        return await _bounded(timeout, work)

    async def confirm_recovery(
        self, recovery_request_id: str, preview_token: str, *, timeout: float,
    ) -> PipelineTaskCheckpoint:
        args = self._recovery_arguments(recovery_request_id)
        if not isinstance(preview_token, str) or not re.fullmatch(_TOKEN_PATTERN, preview_token):
            raise PipelineTaskClientError("invalid_input")

        async def work() -> PipelineTaskCheckpoint:
            result = _record(await self._client.transport.request("tools/call", {
                "name": WIRE["task_recovery"]["confirm"]["name"],
                "arguments": args | {"preview_token": preview_token},
            }, timeout=timeout))
            _check_tool_result(result)
            metadata = _record(_record(result.get("_meta")).get("io.gregale/pipeline-recovery"))
            if (
                result.get("resultType") != "task" or result.get("status") != "working" or not _uuid(result.get("taskId"))
                or result["taskId"] == self._checkpoint["task_id"] or metadata.get("replaces_task_id") != self._checkpoint["task_id"]
                or metadata.get("recovery_request_id") != recovery_request_id or metadata.get("tool") != self._client.tool
                or metadata.get("contract_sha256") != self._client.contract.sha256 or not _uuid(metadata.get("request_id"))
            ):
                raise PipelineTaskClientError("invalid_response")
            return PipelineTaskCheckpoint(
                version=1, tool=self._client.tool, contract_sha256=self._client.contract.sha256,
                task_id=result["taskId"], request_id=metadata["request_id"],
            )

        return await _bounded(timeout, work)

    async def watch(self, *, timeout: float) -> AsyncGenerator[PipelineTaskObservation[O], None]:
        if not _valid_timeout(timeout):
            raise PipelineTaskClientError("invalid_input")
        loop = asyncio.get_running_loop()
        deadline = loop.time() + timeout
        previous = None

        def remaining() -> float:
            left = deadline - loop.time()
            if left <= 0:
                raise PipelineTaskClientError("deadline")
            return left

        def changed(snapshot: PipelineTaskObservation[O]) -> bool:
            nonlocal previous
            fingerprint = job_value_hash(asdict(snapshot))
            different = fingerprint != previous
            previous = fingerprint
            return different

        subscribe = getattr(self._client.transport, "subscribe", None)
        if callable(subscribe):
            for _ in range(2):
                stream: AsyncGenerator[Any, None] = subscribe(self._checkpoint["task_id"], timeout=remaining())
                try:
                    while True:
                        try:
                            async with asyncio.timeout(remaining()):
                                raw = await anext(stream, _EOF)
                        except TimeoutError:
                            raise PipelineTaskClientError("deadline") from None
                        remaining()
                        if raw is _EOF:
                            break
                        snapshot = _observe(raw, self._checkpoint, self._client.contract)
                        if changed(snapshot):
                            yield snapshot
                        if snapshot.status in ("completed", "failed", "cancelled"):
                            return
                except PipelineTaskClientError as error:
                    if error.code == "unsupported":
                        break
                    if error.code != "request_failed":
                        raise
                finally:
                    await stream.aclose()
        while True:
            snapshot = await self.get(timeout=remaining())
            if changed(snapshot):
                yield snapshot
            if snapshot.status in ("completed", "failed", "cancelled"):
                return
            await _bounded(remaining(), lambda: asyncio.sleep(snapshot.poll_interval_ms / 1000))

    async def answer(self, observation: PipelineTaskClarification, key: str, value: R, *, timeout: float) -> None:
        if (
            observation.checkpoint != self._checkpoint
            or observation.status != "input_required"
            or key not in observation.forms
            or not re.fullmatch(_KEY, key)
        ):
            raise PipelineTaskClientError("invalid_input")
        validator = _form_contract(observation.forms[key]["params"])
        reply = self._client.reply_contract
        if reply and job_value_hash(reply.spec["output"]) != job_value_hash(validator.spec["output"]):
            raise PipelineTaskClientError("invalid_response")
        content = _output(validator, value, "invalid_input")
        if _size(content) > 16384:
            raise PipelineTaskClientError("invalid_input")

        async def work() -> None:
            result = _record(
                await self._client.transport.request(
                    "tasks/update",
                    {
                        "taskId": self._checkpoint["task_id"],
                        "inputResponses": {key: {"action": "accept", "content": content}},
                    },
                    timeout=timeout,
                )
            )
            if result.get("resultType") != "complete":
                raise PipelineTaskClientError("invalid_response")

        await _bounded(timeout, work)

    async def read_artifact(
        self, observation: PipelineTaskCompletion[O], name: str, *, timeout: float
    ) -> PipelineTaskResource:
        if observation.checkpoint != self._checkpoint:
            raise PipelineTaskClientError("invalid_input")
        link = next((item for item in observation.artifacts if item["name"] == name), None)
        if link is None:
            raise PipelineTaskClientError("not_found")
        link = _link(link)

        async def work() -> PipelineTaskResource:
            result = _record(
                await self._client.transport.request("resources/read", {"uri": link["uri"]}, timeout=timeout)
            )
            contents = result.get("contents")
            if not isinstance(contents, list) or len(contents) != 1:
                raise PipelineTaskClientError("invalid_response")
            body = _record(contents[0])
            text = body.get("text")
            if (
                body.get("uri") != link["uri"]
                or body.get("mimeType") != link["mimeType"]
                or not isinstance(text, str)
                or len(text.encode("utf-8", errors="surrogatepass")) > 1048576
            ):
                raise PipelineTaskClientError("invalid_response")
            return PipelineTaskResource(uri=link["uri"], mimeType=link["mimeType"], text=text)

        return await _bounded(timeout, work)


async def read_pipeline_task_manifest(transport: PipelineTaskTransport, *, timeout: float) -> PipelineTaskManifest:
    async def work() -> PipelineTaskManifest:
        uri = PIPELINE_TASK_MANIFEST_URI
        result = _record(await transport.request("resources/read", {"uri": uri}, timeout=timeout))
        contents = result.get("contents")
        if not isinstance(contents, list) or len(contents) != 1:
            raise PipelineTaskClientError("invalid_response")
        item = _record(contents[0])
        text = item.get("text")
        if (
            item.get("uri") != uri
            or item.get("mimeType") != "application/json"
            or not isinstance(text, str)
            or len(text.encode("utf-8", errors="surrogatepass")) > PIPELINE_TASK_MANIFEST_MAX_BYTES
        ):
            raise PipelineTaskClientError("invalid_response")
        try:
            return parse_pipeline_task_manifest(json.loads(text))
        except ValueError:
            raise PipelineTaskClientError("invalid_response") from None

    return await _bounded(timeout, work)


class PipelineTaskClient(Generic[I, O, R]):
    def __init__(
        self,
        contract: JobContract[I, O],
        *,
        tool: str,
        transport: PipelineTaskTransport,
        reply_contract: JobContract[Any, R] | None = None,
        check_contract_on_start: bool = False,
    ):
        if (
            not isinstance(tool, str)
            or not re.fullmatch(_NAME, tool)
            or not callable(getattr(transport, "request", None))
        ):
            raise PipelineTaskClientError("invalid_input")
        self.contract, self.tool, self.transport, self.reply_contract = contract, tool, transport, reply_contract
        self._check_contract_on_start = check_contract_on_start

    def _start_input(self, value: I, request_id: str) -> I:
        if not _uuid(request_id):
            raise PipelineTaskClientError("invalid_input")
        try:
            return self.contract.input(value)
        except (JobError, ValueError, TypeError):
            raise PipelineTaskClientError("invalid_input") from None

    async def preview(
        self, value: I, request_id: str, contract: JobContract[None, P], *, timeout: float,
    ) -> PipelineStartPreview[P]:
        data = self._start_input(value, request_id)

        async def work() -> PipelineStartPreview[P]:
            result = _record(await self.transport.request("tools/call", {
                "name": self.tool + "_preview",
                "arguments": {"request_id": request_id, "input": data, "expected_contract_sha256": self.contract.sha256},
            }, timeout=timeout))
            return _read_preview(result, contract)

        return await _bounded(timeout, work)

    async def start(
        self, value: I, request_id: str, *, timeout: float, preview_token: str | None = None,
    ) -> PipelineTaskCheckpoint:
        data = self._start_input(value, request_id)
        if preview_token is not None and (not isinstance(preview_token, str) or not re.fullmatch(_TOKEN_PATTERN, preview_token)):
            raise PipelineTaskClientError("invalid_input")

        async def work() -> PipelineTaskCheckpoint:
            arguments: dict[str, Any] = {"request_id": request_id, "input": data}
            if self._check_contract_on_start or preview_token:
                arguments["expected_contract_sha256"] = self.contract.sha256
            if preview_token:
                arguments["preview_token"] = preview_token
            result = _record(
                await self.transport.request(
                    "tools/call",
                    {
                        "name": self.tool + "_start",
                        "arguments": arguments,
                    },
                    timeout=timeout,
                )
            )
            _check_tool_result(result)
            if (
                result.get("resultType") != "task"
                or result.get("status") != "working"
                or not _uuid(result.get("taskId"))
            ):
                raise PipelineTaskClientError("invalid_response")
            return PipelineTaskCheckpoint(
                version=1,
                tool=self.tool,
                contract_sha256=self.contract.sha256,
                request_id=request_id,
                task_id=result["taskId"],
            )

        return await _bounded(timeout, work)

    def resume(self, value: Any) -> PipelineTaskReader[I, O, R]:
        if (
            not isinstance(value, dict)
            or set(value) != {"version", "tool", "contract_sha256", "request_id", "task_id"}
            or type(value["version"]) is not int
            or value["version"] != 1
            or value["tool"] != self.tool
            or value["contract_sha256"] != self.contract.sha256
            or not _uuid(value["request_id"])
            or not _uuid(value["task_id"])
        ):
            raise PipelineTaskClientError("invalid_input")
        return PipelineTaskReader(self, cast(PipelineTaskCheckpoint, value))

class PipelineTaskHandoffClient(Generic[R]):
    """Reconnect to the named reviewer's one pending clarification."""

    def __init__(self, handoff_id: str, *, reply_contract: JobContract[Any, R], transport: PipelineTaskTransport):
        if not _uuid(handoff_id) or not callable(getattr(transport, "request", None)):
            raise PipelineTaskClientError("invalid_input")
        self._id, self._contract, self._transport = handoff_id, reply_contract, transport

    async def _call(self, name: str, arguments: dict[str, Any], timeout: float) -> dict[str, Any]:
        async def work() -> dict[str, Any]:
            result = _record(await self._transport.request("tools/call", {
                "name": name, "arguments": {"handoff_id": self._id, **arguments},
            }, timeout=timeout))
            _check_tool_result(result, handoff=True)
            body = _record(result.get("structuredContent"))
            if result.get("isError") is not False or body.get("handoff_id") != self._id:
                raise PipelineTaskClientError("invalid_response")
            return body

        return await _bounded(timeout, work)

    async def get(self, *, timeout: float) -> PipelineTaskHandoffView:
        body = await self._call(WIRE["task_handoff"]["read"]["name"], {}, timeout)
        form = _record(body.get("form"))
        params = _record(form.get("params"))
        advertised = _form_contract(params)
        key, expires = body.get("request_key"), body.get("expires_at")
        if (
            form.get("method") != "elicitation/create" or not isinstance(key, str)
            or not re.fullmatch(_KEY, key) or not isinstance(expires, str)
            or job_value_hash(self._contract.spec["output"]) != job_value_hash(advertised.spec["output"])
        ):
            raise PipelineTaskClientError("invalid_response")
        try:
            if datetime.fromisoformat(expires).tzinfo is None:
                raise ValueError
        except (ValueError, OverflowError):
            raise PipelineTaskClientError("invalid_response") from None
        return PipelineTaskHandoffView(
            handoff_id=self._id, request_key=key, expires_at=expires,
            form={"method": "elicitation/create", "params": {
                "mode": "form", "message": params["message"], "requestedSchema": advertised.spec["output"],
            }},
        )

    async def answer(self, view: PipelineTaskHandoffView, value: R, *, timeout: float) -> None:
        if (not isinstance(view, dict) or view.get("handoff_id") != self._id or not isinstance(view.get("request_key"), str)
            or not re.fullmatch(_KEY, view["request_key"])):
            raise PipelineTaskClientError("invalid_input")
        body = await self._call(WIRE["task_handoff"]["reply"]["name"], {
            "request_key": view["request_key"], "input": _output(self._contract, value, "invalid_input"),
        }, timeout)
        if body.get("submitted") is not True:
            raise PipelineTaskClientError("invalid_response")


def _read_handoff_summary(raw: Any, include_approvals: bool) -> PipelineTaskHandoffSummary:
    value = _record(raw)
    handoff_id, expires_at, question = value.get("handoff_id"), value.get("expires_at"), value.get("question")
    approval = include_approvals and value.get("kind") == "approval"
    fields = {"handoff_id", "expires_at", "question", "kind"} if approval else {"handoff_id", "expires_at", "question"}
    if (set(value) != fields or not isinstance(handoff_id, str)
        or not _uuid(handoff_id) or not isinstance(question, str) or not question.strip()
        or len(question.encode("utf-16-le", errors="surrogatepass")) > 2048 or not isinstance(expires_at, str)):
        raise PipelineTaskClientError("invalid_response")
    try:
        if datetime.fromisoformat(expires_at).tzinfo is None:
            raise ValueError
    except (ValueError, OverflowError):
        raise PipelineTaskClientError("invalid_response") from None
    item = PipelineTaskHandoffSummary(handoff_id=handoff_id, expires_at=expires_at, question=question)
    if approval:
        item["kind"] = "approval"
    return item


async def _poll_reviewer_inbox(sink: LiveWorkInboxSink) -> None:
    sink.ready()
    await asyncio.Future[None]()


class PipelineTaskReviewerInbox:
    """List and watch the authenticated reviewer's pending clarifications."""

    def __init__(self, transport: PipelineTaskTransport):
        if not callable(getattr(transport, "request", None)):
            raise PipelineTaskClientError("invalid_input")
        self._transport = transport

    async def page(
        self, *, timeout: float, page_size: int = 20, cursor: str | None = None, include_approvals: bool = False,
    ) -> PipelineTaskHandoffPage:
        if (type(include_approvals) is not bool or type(page_size) is not int or not 1 <= page_size <= _PAGE_LIMIT
            or (cursor is not None and (not isinstance(cursor, str) or not _CURSOR.fullmatch(cursor)))):
            raise PipelineTaskClientError("invalid_input")

        async def work() -> PipelineTaskHandoffPage:
            arguments: dict[str, Any] = {"page_size": page_size}
            if cursor is not None:
                arguments["cursor"] = cursor
            if include_approvals:
                arguments["include_approvals"] = True
            result = _record(await self._transport.request("tools/call", {
                "name": WIRE["task_handoff"]["list"]["name"], "arguments": arguments,
            }, timeout=timeout))
            _check_tool_result(result, handoff=True)
            body = _record(result.get("structuredContent"))
            items, following = body.get("items"), body.get("next_cursor")
            if (result.get("isError") is not False or set(body) != {"items", "next_cursor"}
                or not isinstance(items, list) or len(items) > page_size
                or (following is not None and (not isinstance(following, str)
                    or not _CURSOR.fullmatch(following) or following == cursor))):
                raise PipelineTaskClientError("invalid_response")
            entries = [_read_handoff_summary(item, include_approvals) for item in items]
            if len({item["handoff_id"] for item in entries}) != len(entries):
                raise PipelineTaskClientError("invalid_response")
            return PipelineTaskHandoffPage(items=entries, next_cursor=following)

        return await _bounded(timeout, work)

    def watch(
        self, *, timeout: float, page_size: int = 20, max_pages: int = 10, include_approvals: bool = False,
        max_snapshots: int = 1000, refresh_interval: float = 15,
        notifications: tuple[str, LiveWorkInboxSource] | None = None,
    ) -> AsyncIterator[PipelineTaskHandoffSnapshot]:
        if type(max_pages) is not int or not 1 <= max_pages <= 20:
            raise PipelineTaskClientError("invalid_input")
        resource_id, source = notifications if notifications is not None else ("reviewer-inbox", _poll_reviewer_inbox)

        async def load_snapshot(remaining: float) -> PipelineTaskHandoffSnapshot:
            loop = asyncio.get_running_loop()
            deadline = loop.time() + remaining
            entries: dict[str, PipelineTaskHandoffSummary] = {}
            cursors: set[str] = set()
            cursor = None
            try:
                for _ in range(max_pages):
                    left = deadline - loop.time()
                    if left <= 0:
                        raise LiveWorkInboxError("deadline")
                    result = await self.page(page_size=page_size, cursor=cursor, timeout=left, include_approvals=include_approvals)
                    for entry in result["items"]:
                        if entry["handoff_id"] in entries:
                            raise LiveWorkInboxError("invalid_snapshot")
                        entries[entry["handoff_id"]] = entry
                    cursor = result["next_cursor"]
                    if cursor is None:
                        break
                    if cursor in cursors:
                        raise LiveWorkInboxError("invalid_snapshot")
                    cursors.add(cursor)
            except PipelineTaskClientError as error:
                if error.code in ("unauthorized", "not_found"):
                    raise LiveWorkInboxError("unauthorized") from None
                if error.code == "invalid_response":
                    raise LiveWorkInboxError("invalid_snapshot") from None
                raise
            return PipelineTaskHandoffSnapshot(items=list(entries.values()), truncated=cursor is not None)

        return _watch_inbox_snapshots(
            target={"resource_id": resource_id, "business_ref": "reviewer-inbox"},
            source=source, load_snapshot=load_snapshot, parse=lambda snapshot, _: snapshot,
            timeout=timeout, refresh_interval=refresh_interval, max_snapshots=max_snapshots,
            retry_read=lambda error: isinstance(error, PipelineTaskClientError) and error.code == "request_failed",
        )

_Decision = TypeVar("_Decision")


class PipelineTaskApprovalClient(Generic[R, _Decision]):
    """Preview the bound action and submit an explicit reviewer decision."""

    def __init__(self, handoff_id: str, *, contract: JobContract[R, _Decision], transport: PipelineTaskTransport):
        if not _uuid(handoff_id) or not callable(getattr(transport, "request", None)):
            raise PipelineTaskClientError("invalid_input")
        self._id, self._contract, self._transport = handoff_id, contract, transport

    async def _call(self, name: str, arguments: dict[str, Any], timeout: float) -> dict[str, Any]:
        async def work() -> dict[str, Any]:
            result = _record(await self._transport.request("tools/call", {
                "name": name, "arguments": {"handoff_id": self._id, **arguments},
            }, timeout=timeout))
            _check_tool_result(result, handoff=True)
            body = _record(result.get("structuredContent"))
            if result.get("isError") is not False or body.get("handoff_id") != self._id:
                raise PipelineTaskClientError("invalid_response")
            return body
        return await _bounded(timeout, work)

    async def get(self, *, timeout: float) -> PipelineTaskApprovalView[R]:
        body = await self._call(WIRE["task_handoff"]["approval"]["name"], {}, timeout)
        title, expires = body.get("title"), body.get("expires_at")
        key, digest = body.get("decision_key"), body.get("review_sha256")
        if (body.get("contract_sha256") != self._contract.sha256 or not isinstance(key, str) or not _hash(key) or not _hash(digest)
            or not isinstance(title, str) or not title.strip()
            or len(title.encode("utf-16-le", errors="surrogatepass")) > 2048 or not isinstance(expires, str)):
            raise PipelineTaskClientError("invalid_response")
        try:
            if datetime.fromisoformat(expires).tzinfo is None:
                raise ValueError
            review = self._contract.input(body["review"])
            if job_value_hash(review) != digest:
                raise ValueError
        except (JobError, KeyError, ValueError, OverflowError):
            raise PipelineTaskClientError("invalid_response") from None
        return PipelineTaskApprovalView(
            handoff_id=self._id, expires_at=expires, title=title, review=review,
            contract_sha256=self._contract.sha256, review_sha256=digest, decision_key=key,
        )

    async def decide(self, view: PipelineTaskApprovalView[R], state: PipelineTaskApprovalChoice,
                     value: _Decision, *, timeout: float) -> PipelineTaskApprovalReceipt:
        if (not isinstance(view, dict) or view.get("handoff_id") != self._id
            or view.get("contract_sha256") != self._contract.sha256 or not _hash(view.get("decision_key"))
            or state not in ("approved", "rejected")):
            raise PipelineTaskClientError("invalid_input")
        try:
            if job_value_hash(self._contract.input(view["review"])) != view["review_sha256"]:
                raise ValueError
            payload = self._contract.output(value)
        except (JobError, KeyError, ValueError):
            raise PipelineTaskClientError("invalid_input") from None
        body = await self._call(WIRE["task_handoff"]["decide"]["name"], {
            "state": state, "input": payload, "decision_key": view["decision_key"],
        }, timeout)
        decided_at = body.get("decided_at")
        if body.get("state") != state or not isinstance(decided_at, str):
            raise PipelineTaskClientError("invalid_response")
        try:
            if datetime.fromisoformat(decided_at).tzinfo is None:
                raise ValueError
        except (ValueError, OverflowError):
            raise PipelineTaskClientError("invalid_response") from None
        return PipelineTaskApprovalReceipt(handoff_id=self._id, state=state, decided_at=decided_at)
