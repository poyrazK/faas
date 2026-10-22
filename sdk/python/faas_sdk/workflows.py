"""Code-first workflows (ADR-20261021204307).

A workflow is an ``async`` function that calls ``wf.step``, ``wf.sleep`` and
``wf.wait_for_event``. Gregale calls the workflow's entry handler at every
durable boundary with the run's journal. Recorded operations return their
stored result; the first new ones run (steps) or are reported (waits), and the
function is suspended until Gregale calls again. Code outside steps must
therefore be deterministic and must not await I/O.

    from faas_sdk.workflows import serve_workflows, workflow

    @workflow("order_followup")
    async def order_followup(order, wf):
        charge = await wf.step("charge", lambda ctx: charge_card(order["id"], ctx.idempotency_key))
        await wf.sleep("cool_off", "3d")
        approval = await wf.wait_for_event("approval", event="approved", timeout="7d")
        return {"charge": charge, "approved": approval is not None}

    app = serve_workflows([order_followup])  # an ASGI app; mount it at the entry path

Durations are seconds (``int``/``float``), ``datetime.timedelta`` or strings
such as ``"30s"``, ``"15m"``, ``"2h"`` and ``"3d"``.
"""

from __future__ import annotations

import asyncio
import contextlib
import inspect
import json
import re
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from datetime import timedelta
from typing import Any

WORKFLOW_CODE_PROTOCOL = "gregale.workflow.code/v1"

_OP_NAME = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.:#/-]{0,127}$")
_DURATION = re.compile(r"^(\d+(?:\.\d+)?)(ms|s|m|h|d)$")
_UNITS_MS = {"ms": 1, "s": 1_000, "m": 60_000, "h": 3_600_000, "d": 86_400_000}
_MAX_REQUEST_BYTES = 8 << 20
WORKFLOW_RESPONSE_MAX_BYTES = 960 * 1024
"""One call's answer must fit the platform's 960 KiB response bound."""

Duration = int | float | str | timedelta


class NonRetryableError(Exception):
    """Fails a step without retrying it."""


class WorkflowStepError(Exception):
    """Raised into the workflow when a step has exhausted its attempts."""

    def __init__(self, step: str, message: str, attempts: int) -> None:
        super().__init__(message)
        self.step = step
        self.attempts = attempts


class UnknownWorkflowError(LookupError):
    def __init__(self, workflow_name: str) -> None:
        super().__init__(f'no workflow named "{workflow_name}" is served here')
        self.workflow = workflow_name


@dataclass(frozen=True)
class RetryPolicy:
    """Total attempts (1-25) and back-off for a step."""

    max_attempts: int
    backoff: str | None = None

    def wire(self) -> dict[str, Any]:
        value: dict[str, Any] = {"max_attempts": self.max_attempts}
        if self.backoff:
            value["backoff"] = self.backoff
        return value


@dataclass(frozen=True)
class StepContext:
    attempt: int
    """1 on the first attempt."""
    idempotency_key: str
    """``workflow/<run-id>/<step>``, stable across attempts."""


@dataclass(frozen=True)
class WorkflowDefinition:
    name: str
    run: Callable[[Any, WorkflowContext], Awaitable[Any]]


def workflow(name: str) -> Callable[[Callable[[Any, WorkflowContext], Awaitable[Any]]], WorkflowDefinition]:
    """Declares a code-first workflow; ``name`` matches ``gregale.yaml``."""
    if not name:
        raise ValueError("workflow name is required")

    def decorate(fn: Callable[[Any, WorkflowContext], Awaitable[Any]]) -> WorkflowDefinition:
        if not inspect.iscoroutinefunction(fn):
            raise TypeError("a workflow function must be async")
        return WorkflowDefinition(name=name, run=fn)

    return decorate


def parse_duration_ms(value: Duration) -> int:
    """Converts a Duration to milliseconds; bare numbers are seconds."""
    if isinstance(value, timedelta):
        return round(value.total_seconds() * 1000)
    if isinstance(value, bool):
        raise ValueError(f"invalid duration {value!r}")
    if isinstance(value, (int, float)):
        if value < 0 or value != value:  # NaN
            raise ValueError(f"invalid duration {value!r}")
        return round(value * 1000)
    match = _DURATION.match(value.strip())
    if not match:
        raise ValueError(f'invalid duration "{value}"; use seconds or a value such as "30s", "15m", "2h" or "3d"')
    return round(float(match.group(1)) * _UNITS_MS[match.group(2)])


def _message(error: BaseException) -> str:
    return str(error) or type(error).__name__


def _json_value(value: Any) -> Any:
    return json.loads(json.dumps(value))


class WorkflowContext:
    """The ``wf`` argument of a workflow function."""

    def __init__(self, request: Mapping[str, Any]) -> None:
        self._request = request
        self._journal: Sequence[Mapping[str, Any]] = request["journal"]
        self._seq = 0
        self._names: dict[str, int] = {}
        self.ops: list[dict[str, Any]] = []
        self.pending: list[asyncio.Future[None]] = []
        self.activity = 0

    @property
    def run_id(self) -> str:
        return str(self._request["run_id"])

    @property
    def workflow(self) -> str:
        return str(self._request["workflow"])

    @property
    def invocation(self) -> int:
        return int(self._request.get("invocation", 1))

    def _claim(self, name: str, kind: str, event: str | None = None) -> tuple[int, str, Mapping[str, Any] | None, bool]:
        if not _OP_NAME.match(name):
            raise ValueError(f'invalid workflow operation name "{name}": use 1-128 of A-Z a-z 0-9 _ . : # / - starting with a letter or digit')
        count = self._names.get(name, 0) + 1
        self._names[name] = count
        op_name = name if count == 1 else f"{name}#{count}"
        seq = self._seq
        self._seq += 1
        entry = self._journal[seq] if seq < len(self._journal) else None
        conflict = entry is not None and (
            entry.get("name") != op_name or entry.get("kind") != kind or (kind == "event" and entry.get("event") != event)
        )
        return seq, op_name, None if conflict else entry, conflict

    def _report(self, op: dict[str, Any]) -> None:
        self.ops.append(op)
        self.activity += 1

    @staticmethod
    async def _suspend() -> Any:
        # A future nobody resolves: the function waits here until Gregale
        # calls again with the operation's result.
        await asyncio.get_running_loop().create_future()

    async def step(self, name: str, fn: Callable[[StepContext], Any], *, retry: RetryPolicy | None = None) -> Any:
        """Runs ``fn`` (sync or async) and records its JSON result."""
        seq, op_name, entry, conflict = self._claim(name, "step")
        if conflict:
            self._report({"seq": seq, "name": op_name, "kind": "step", "status": "succeeded"})
            return await self._suspend()
        if entry is not None and entry.get("status") == "succeeded":
            return entry.get("output")
        if entry is not None and entry.get("status") == "failed":
            raise WorkflowStepError(op_name, entry.get("error") or "step failed", int(entry.get("attempt") or 1))
        attempt = int((entry or {}).get("attempt") or 0) + 1
        context = StepContext(attempt=attempt, idempotency_key=f"workflow/{self.run_id}/{op_name}")
        self.activity += 1
        self.pending.append(asyncio.ensure_future(self._run_step(seq, op_name, fn, context, retry)))
        return await self._suspend()

    async def _run_step(self, seq: int, name: str, fn: Callable[[StepContext], Any], context: StepContext, retry: RetryPolicy | None) -> None:
        op: dict[str, Any] = {"seq": seq, "name": name, "kind": "step"}
        if retry is not None:
            op["retry"] = retry.wire()
        try:
            value = fn(context)
            if inspect.isawaitable(value):
                value = await value
        except Exception as error:  # noqa: BLE001 - a step failure is data
            op.update(status="failed", error=_message(error))
            if isinstance(error, NonRetryableError):
                op["non_retryable"] = True
            self._report(op)
            return
        try:
            output = _json_value(value)
        except (TypeError, ValueError) as error:
            op.update(status="failed", error=f"step result is not JSON: {_message(error)}", non_retryable=True)
            self._report(op)
            return
        op["status"] = "succeeded"
        if output is not None:
            op["output"] = output
        self._report(op)

    async def sleep(self, name: str, duration: Duration) -> None:
        """Parks the run; its app may scale to zero meanwhile."""
        seq, op_name, entry, conflict = self._claim(name, "sleep")
        if entry is not None and entry.get("status") == "succeeded":
            return
        if entry is None:
            self._report({"seq": seq, "name": op_name, "kind": "sleep", "duration_ms": 1_000 if conflict else parse_duration_ms(duration)})
        await self._suspend()

    async def wait_for_event(self, name: str, *, event: str | None = None, timeout: Duration | None = None) -> Any | None:
        """Returns the next event of that name sent to the run, or None on timeout."""
        event_name = event or name
        seq, op_name, entry, conflict = self._claim(name, "event", event_name)
        if entry is not None and entry.get("status") == "succeeded":
            return None if entry.get("timed_out") else entry.get("output")
        if entry is None:
            op: dict[str, Any] = {"seq": seq, "name": op_name, "kind": "event", "event": event_name}
            if timeout is not None and not conflict:
                op["timeout_ms"] = parse_duration_ms(timeout)
            self._report(op)
        return await self._suspend()


def _parse_request(body: Any) -> Mapping[str, Any]:
    value = json.loads(body) if isinstance(body, (str, bytes, bytearray)) else body
    if not isinstance(value, Mapping) or value.get("protocol") != WORKFLOW_CODE_PROTOCOL:
        raise ValueError(f"expected a {WORKFLOW_CODE_PROTOCOL} request")
    if not isinstance(value.get("workflow"), str) or not isinstance(value.get("run_id"), str) or not isinstance(value.get("journal"), list):
        raise ValueError("workflow request is missing workflow, run_id or journal")
    return value


async def run_workflow_invocation(definitions: WorkflowDefinition | Sequence[WorkflowDefinition], body: Any) -> dict[str, Any]:
    """Runs one invocation against its journal and returns the response envelope."""
    request = _parse_request(body)
    items = [definitions] if isinstance(definitions, WorkflowDefinition) else list(definitions)
    definition = next((item for item in items if item.name == request["workflow"]), None)
    if definition is None:
        raise UnknownWorkflowError(request["workflow"])
    wf = WorkflowContext(request)
    task = asyncio.ensure_future(definition.run(request.get("input"), wf))
    # Suspend once the function has finished or nothing can make progress:
    # every started step has settled and the loop went idle without activity.
    while True:
        before, steps = wf.activity, len(wf.pending)
        if wf.pending:
            await asyncio.gather(*wf.pending, return_exceptions=True)
        for _ in range(5):
            await asyncio.sleep(0)
        if len(wf.pending) == steps and (task.done() or wf.activity == before):
            break
    if wf.pending:
        await asyncio.gather(*wf.pending, return_exceptions=True)
    result: dict[str, Any] = {"status": "running"}
    if task.done():
        error = task.exception()
        if error is not None:
            result = {"status": "failed", "error": _message(error)}
        else:
            try:
                output = _json_value(task.result())
                result = {"status": "succeeded"} if output is None else {"status": "succeeded", "output": output}
            except (TypeError, ValueError) as json_error:
                result = {"status": "failed", "error": f"workflow result is not JSON: {_message(json_error)}"}
    else:
        task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await task
    return _fit_response({"protocol": WORKFLOW_CODE_PROTOCOL, "ops": sorted(wf.ops, key=lambda op: op["seq"]), "result": result})


def _byte_length(value: Any) -> int:
    return len(json.dumps(value).encode())


def _fit_response(response: dict[str, Any]) -> dict[str, Any]:
    """Fails, without retry, results that would overflow one call's answer."""
    budget = WORKFLOW_RESPONSE_MAX_BYTES - 1024
    used = _byte_length({"protocol": response["protocol"], "ops": [], "result": {"status": "running"}})
    fitted = []
    for op in response["ops"]:
        size = _byte_length(op) + 1
        if op["kind"] == "step" and op.get("status") == "succeeded" and used + size > budget:
            op = {key: value for key, value in op.items() if key != "output"}
            op.update(
                status="failed",
                error=f"step result does not fit in one call's {WORKFLOW_RESPONSE_MAX_BYTES} byte answer; store large data elsewhere and return its key",
                non_retryable=True,
            )
            size = _byte_length(op) + 1
        used += size
        fitted.append(op)
    response["ops"] = fitted
    if response["result"]["status"] == "succeeded" and used + _byte_length(response["result"]) > budget:
        response["result"] = {"status": "failed", "error": f"workflow output does not fit in one call's {WORKFLOW_RESPONSE_MAX_BYTES} byte answer"}
    return response


def serve_workflows(definitions: WorkflowDefinition | Sequence[WorkflowDefinition], *, allow_unverified_callers: bool = False) -> Callable[..., Awaitable[None]]:
    """Returns an ASGI app serving the workflows' entry protocol.

    Gregale strips ``x-faas-*`` headers from public requests, so by default the
    app refuses callers other than the platform. ``allow_unverified_callers``
    is for local testing only.
    """

    async def app(scope: Mapping[str, Any], receive: Callable[[], Awaitable[Mapping[str, Any]]], send: Callable[[Mapping[str, Any]], Awaitable[None]]) -> None:
        if scope["type"] == "lifespan":
            while True:
                message = await receive()
                if message["type"] == "lifespan.startup":
                    await send({"type": "lifespan.startup.complete"})
                elif message["type"] == "lifespan.shutdown":
                    await send({"type": "lifespan.shutdown.complete"})
                    return
        if scope["type"] != "http":
            return
        status, payload = await _handle(scope, receive)
        await send({"type": "http.response.start", "status": status, "headers": [(b"content-type", b"application/json")]})
        await send({"type": "http.response.body", "body": json.dumps(payload).encode()})

    async def _handle(scope: Mapping[str, Any], receive: Callable[[], Awaitable[Mapping[str, Any]]]) -> tuple[int, Any]:
        if scope.get("method") != "POST":
            return 405, {"error": "method not allowed"}
        headers = {key.decode("latin-1").lower(): value.decode("latin-1") for key, value in scope.get("headers", [])}
        if not allow_unverified_callers and (headers.get("x-faas-workflow-mode") != "code" or not headers.get("x-faas-workflow-run-id")):
            return 403, {"error": "workflow entry handlers only accept Gregale workflow calls"}
        body = bytearray()
        while True:
            message = await receive()
            body.extend(message.get("body", b""))
            if len(body) > _MAX_REQUEST_BYTES:
                return 413, {"error": "workflow request too large"}
            if not message.get("more_body"):
                break
        try:
            parsed = json.loads(body)
        except ValueError:
            return 400, {"error": "workflow request is not JSON"}
        try:
            return 200, await run_workflow_invocation(definitions, parsed)
        except UnknownWorkflowError as error:
            return 404, {"error": str(error)}
        except ValueError as error:
            return 400, {"error": str(error)}

    return app


@dataclass
class LocalRunResult:
    status: str
    output: Any = None
    error: str | None = None
    journal: list[dict[str, Any]] = field(default_factory=list)
    invocations: int = 0


async def run_workflow_locally(
    definition: WorkflowDefinition,
    input: Any = None,  # noqa: A002 - mirrors the workflow argument
    *,
    events: Mapping[str, Sequence[Any]] | None = None,
    retry: RetryPolicy | None = None,
    max_invocations: int = 1000,
    run_id: str = "local-run",
) -> LocalRunResult:
    """Runs a workflow to completion in-process, the way Gregale would.

    Sleeps finish at once, waits take ``events`` in order or time out, and
    failed steps retry under their policy. Use it in unit tests.
    """
    journal: list[dict[str, Any]] = []
    consumed: dict[str, int] = {}
    default_attempts = (retry or RetryPolicy(max_attempts=3)).max_attempts
    for invocation in range(1, max_invocations + 1):
        response = await run_workflow_invocation(definition, {
            "protocol": WORKFLOW_CODE_PROTOCOL, "run_id": run_id, "workflow": definition.name,
            "input": input, "invocation": invocation, "journal": [dict(entry) for entry in journal],
        })
        for op in response["ops"]:
            seq = op["seq"]
            recorded = journal[seq] if seq < len(journal) else None
            if (recorded is not None and (recorded["name"] != op["name"] or recorded["kind"] != op["kind"])) or (recorded is None and seq != len(journal)):
                return LocalRunResult("failed", error=f'nondeterministic workflow: operation {seq} "{op["name"]}"', journal=journal, invocations=invocation)
            entry: dict[str, Any] = {"seq": seq, "name": op["name"], "kind": op["kind"]}
            if op["kind"] == "step":
                attempt = int((recorded or {}).get("attempt") or 0) + 1
                max_attempts = int((op.get("retry") or {}).get("max_attempts") or default_attempts)
                entry["attempt"] = attempt
                if op["status"] == "succeeded":
                    entry["status"] = "succeeded"
                    if "output" in op:
                        entry["output"] = op["output"]
                else:
                    entry["status"] = "retrying" if not op.get("non_retryable") and attempt < max_attempts else "failed"
                    entry["error"] = op.get("error") or "step failed"
            elif op["kind"] == "sleep":
                entry["status"] = "succeeded"
            else:
                event_name = op.get("event") or op["name"]
                index = consumed.get(event_name, 0)
                consumed[event_name] = index + 1
                payloads = list((events or {}).get(event_name, ()))
                entry.update(event=event_name, status="succeeded")
                if index < len(payloads):
                    entry["output"] = _json_value(payloads[index])
                else:
                    entry["timed_out"] = True
            if recorded is None:
                journal.append(entry)
            else:
                journal[seq] = entry
        result = response["result"]
        if result["status"] == "succeeded":
            return LocalRunResult("succeeded", output=result.get("output"), journal=journal, invocations=invocation)
        if result["status"] == "failed":
            return LocalRunResult("failed", error=result.get("error") or "workflow function failed", journal=journal, invocations=invocation)
        if not response["ops"]:
            return LocalRunResult("failed", error="workflow made no progress", journal=journal, invocations=invocation)
    return LocalRunResult("failed", error=f"workflow did not finish within {max_invocations} invocations", journal=journal, invocations=max_invocations)
