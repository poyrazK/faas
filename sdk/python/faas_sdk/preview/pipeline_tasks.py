"""Durable MCP Task views over the existing typed pipeline lifecycle."""

from __future__ import annotations

import asyncio
import copy
import hashlib
import hmac
import re
import uuid
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Any, Generic, Literal, NotRequired, Protocol, TypedDict, TypeVar, cast

from ..job_runtime import job_value_hash
from ._pipeline_tool_protocol import PIPELINE_TASK_RECOVERY_SCHEMA as PIPELINE_TASK_RECOVERY_SCHEMA
from ._pipeline_tool_protocol import PIPELINE_TASK_SCHEMA as PIPELINE_TASK_SCHEMA
from ._pipeline_tool_protocol import SQL, WIRE
from .application_toolkit import ApplicationToolkit, ApplicationToolkitError, ToolkitAccess
from .live_work_inbox import _check_review
from .pipeline_gates import PipelineGateView
from .pipeline_start_preview import _TOKEN_PATTERN
from .pipeline_state import PinnedPipelineSourceFailure, _hash, _is_pinned_source_failure, _uuid
from .pipeline_task_approvals import PipelineTaskApproval, PipelineTaskApprovalChoice, PipelineTaskApprovalView
from .pipeline_task_handoffs import (
    _PAGE_LIMIT,
    _POSITION,
    _SCAN_LIMIT,
    PipelineTaskHandoff,
    PipelineTaskHandoffPage,
    PipelineTaskHandoffRecord,
    PipelineTaskHandoffSettings,
    PipelineTaskHandoffSummary,
    _after,
    _approval_key,
    _cursor,
    _parse_handoff,
    _reviewer,
)
from .pipeline_task_input import PipelineTaskFormRequest, PipelineTaskInputForm, _form_key, _Gate, _parse_form_reply
from .pipeline_task_manifest import PIPELINE_TASK_MANIFEST_URI, _build_manifest, _json
from .pipeline_task_recovery import (
    PipelineTaskRecoveryPolicy,
    PipelineTaskRecoverySource,
    _recovery_request,
    _recovery_request_id,
)
from .query_tools import QueryToolError, _query_scope
from .work_inbox import WorkInboxReview
from .work_inbox_follow_up import WorkInboxToolCall

A = TypeVar("A")
T = TypeVar("T")
_MAX_TTL = 7 * 86_400_000
_TASK_ID = re.compile(r"[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}")
_STATES = ("start_unknown", "running", "awaiting_approval", "awaiting_input", "succeeded", "failed", "cancelled")


@dataclass(frozen=True, slots=True)
class PipelineTaskCaller(Generic[A]):
    resource: str
    subject: str
    client_id: str
    access: ToolkitAccess[A]
    supports_forms: bool = False


class _NewBinding(TypedDict):
    task_id: str
    tool_name: str
    request_id: str
    receipt_identity: str
    ttl_ms: int


class _NewLinkedBinding(_NewBinding):
    replaces_task_id: str


class PipelineTaskBinding(_NewBinding):
    created_at: str
    replaces_task_id: NotRequired[str]


class _TaskID(TypedDict):
    task_id: str


class _TaskRequest(TypedDict):
    tool_name: str
    request_id: str


_Lookup = _TaskID | _TaskRequest


class PipelineTaskStore(Protocol):
    async def load(self, namespace: str, owner: str, lookup: _Lookup) -> PipelineTaskBinding | None: ...

    async def bind(self, namespace: str, owner: str, row: _NewBinding) -> PipelineTaskBinding | None: ...


class _TaskFrame(TypedDict):
    taskId: str
    createdAt: str
    lastUpdatedAt: str
    ttlMs: int
    pollIntervalMs: int
    statusMessage: str


class _PipelineReplacement(TypedDict):
    recovery_request_id: str
    replaces_task_id: str
    request_id: str
    tool: str
    contract_sha256: str


class PipelineTaskHandle(_TaskFrame):
    resultType: Literal["task"]
    status: Literal["working"]


class _LinkedTaskHandle(PipelineTaskHandle):
    _meta: dict[str, _PipelineReplacement]


class _Step(TypedDict):
    name: str
    status: str
    attempt: int


class PipelineTaskProgress(TypedDict):
    replaces_task_id: NotRequired[str]
    source_failure: NotRequired[PinnedPipelineSourceFailure]
    request_id: str
    run_id: str | None
    state: Literal[
        "start_unknown", "running", "awaiting_approval", "awaiting_input", "succeeded", "failed", "cancelled"
    ]
    steps: list[_Step]
    review: NotRequired[WorkInboxReview]
    follow_up: NotRequired[WorkInboxToolCall]


class PipelineTaskSnapshot(_TaskFrame):
    resultType: Literal["complete"]
    status: Literal["working", "cancelled", "completed", "failed", "input_required"]
    _meta: dict[str, PipelineTaskProgress]
    inputRequests: NotRequired[dict[str, PipelineTaskFormRequest]]
    result: NotRequired[dict[str, Any]]
    error: NotRequired[dict[str, Any]]


class PostgresPipelineTaskStore:
    def __init__(self, query: Callable[[str, list[object]], Awaitable[Sequence[PipelineTaskBinding]]]):
        self._query = query

    async def _one(self, key: str, args: list[object]) -> PipelineTaskBinding | None:
        rows = await self._query(SQL[key], args)
        if len(rows) > 1:
            raise QueryToolError("invalid_output")
        if not rows:
            return None
        row = cast(PipelineTaskBinding, dict(rows[0]))
        if row.get("replaces_task_id") is None:
            row.pop("replaces_task_id", None)
        return row

    async def _load(self, namespace: str, owner: str, lookup: _Lookup, linked: bool) -> PipelineTaskBinding | None:
        prefix = "task_recovery" if linked else "task"
        if "task_id" in lookup:
            return await self._one(prefix + "_by_id", [namespace, owner, cast(_TaskID, lookup)["task_id"]])
        return await self._one(prefix + "_by_request", [namespace, owner, lookup["tool_name"], lookup["request_id"]])

    async def load(self, namespace: str, owner: str, lookup: _Lookup) -> PipelineTaskBinding | None:
        return await self._load(namespace, owner, lookup, False)

    async def load_linked(self, namespace: str, owner: str, lookup: _Lookup) -> PipelineTaskBinding | None:
        return await self._load(namespace, owner, lookup, True)

    @staticmethod
    def _args(namespace: str, owner: str, row: _NewBinding) -> list[object]:
        return [namespace, owner, row["tool_name"], row["request_id"], row["receipt_identity"], row["task_id"], row["ttl_ms"]]

    async def bind(self, namespace: str, owner: str, row: _NewBinding) -> PipelineTaskBinding | None:
        return await self._one("task_bind", self._args(namespace, owner, row))

    async def bind_linked(self, namespace: str, owner: str, row: _NewLinkedBinding) -> PipelineTaskBinding | None:
        return await self._one("task_recovery_bind", [*self._args(namespace, owner, row), row["replaces_task_id"]])


class _LinkedTaskStore(PipelineTaskStore, Protocol):
    async def load_linked(self, namespace: str, owner: str, lookup: _Lookup) -> PipelineTaskBinding | None: ...
    async def bind_linked(self, namespace: str, owner: str, row: _NewLinkedBinding) -> PipelineTaskBinding | None: ...


def _task_id(value: Any) -> bool:
    return isinstance(value, str) and _TASK_ID.fullmatch(value) is not None


def _positive(value: Any, maximum: int) -> bool:
    return type(value) is int and 0 < value <= maximum


def _receipt(result: dict[str, Any], tool: str, request_id: str) -> tuple[dict[str, Any], str, str | None]:
    content = result.get("structuredContent")
    if result.get("isError") or not isinstance(content, dict) or content.get("state") == "not_found":
        raise QueryToolError("not_found")
    value = content.get("receipt")
    if (
        not isinstance(value, dict)
        or value.get("tool") != tool
        or value.get("request_id") != request_id
        or not _hash(value.get("identity"))
        or "run_id" not in value
        or (value["run_id"] is not None and not _uuid(value["run_id"]))
    ):
        raise QueryToolError("invalid_output")
    return content, value["identity"], value["run_id"]


class PipelineTasks(Generic[A]):
    def __init__(
        self,
        toolkit: ApplicationToolkit[A],
        *,
        namespace: str,
        owner_key: str,
        tools: Sequence[str],
        store: PipelineTaskStore,
        ttl_ms: int = 86400000,
        poll_interval_ms: int = 2000,
        publish_manifest: bool = False,
        input_forms: Mapping[str, PipelineTaskInputForm[A]] | None = None,
        recovery: PipelineTaskRecoveryPolicy[A] | None = None,
        handoffs: PipelineTaskHandoffSettings | None = None,
        approval_handoffs: Mapping[str, PipelineTaskApproval[Any, Any, A]] | None = None,
    ):
        if (
            not isinstance(namespace, str)
            or not re.fullmatch(r"[A-Za-z0-9_.:-]{1,96}", namespace)
            or not isinstance(owner_key, str)
            or len(owner_key.encode()) < 32
            or not _positive(ttl_ms, _MAX_TTL)
            or not _positive(poll_interval_ms, 60000)
            or isinstance(tools, str)
            or not 1 <= len(tools) <= 32
        ):
            raise QueryToolError("invalid_input")
        self._starts: dict[str, str] = {}
        published = toolkit.tool_scopes
        for tool in tools:
            if not isinstance(tool, str) or not re.fullmatch(r"[a-z][a-z0-9_]{0,63}", tool):
                raise QueryToolError("invalid_input")
            name = tool + "_start"
            if name in self._starts or any(f"{tool}_{op}" not in published for op in ("start", "status", "result")):
                raise QueryToolError("invalid_input")
            self._starts[name] = tool
        self._forms = dict(input_forms or {})
        if any(
            tool + "_start" not in self._starts or not isinstance(form, PipelineTaskInputForm)
            for tool, form in self._forms.items()
        ):
            raise QueryToolError("invalid_input")
        self._toolkit, self._namespace, self._key, self._store = toolkit, namespace, owner_key.encode(), store
        self._ttl_ms, self._poll_interval = ttl_ms, poll_interval_ms
        self._recovery = recovery
        self._linked_store: _LinkedTaskStore | None = None
        self._recovery_scopes: tuple[str, ...] = ()
        if recovery is not None:
            if (not isinstance(recovery, PipelineTaskRecoveryPolicy) or not callable(recovery.prepare)
                or isinstance(recovery.scopes, str) or not recovery.scopes
                or any(not isinstance(scope, str) or not re.fullmatch(r"[\x21\x23-\x5b\x5d-\x7e]{1,128}", scope) for scope in recovery.scopes)
                or len(set(recovery.scopes)) != len(recovery.scopes)
                or any(not callable(getattr(store, method, None)) for method in ("load_linked", "bind_linked"))):
                raise QueryToolError("invalid_input")
            self._linked_store = cast(_LinkedTaskStore, store)
            self._recovery_scopes = tuple(recovery.scopes)
        self._handoffs = handoffs
        self._handoff_scopes: tuple[str, ...] = ()
        if handoffs is not None:
            if (
                not isinstance(handoffs, PipelineTaskHandoffSettings)
                or isinstance(handoffs.scopes, str) or not handoffs.scopes
                or any(not isinstance(scope, str) or not re.fullmatch(r"[\x21\x23-\x5b\x5d-\x7e]{1,128}", scope) for scope in handoffs.scopes)
                or len(set(handoffs.scopes)) != len(handoffs.scopes)
                or any(not callable(getattr(handoffs.store, method, None)) for method in ("grant", "load", "revoke"))
            ):
                raise QueryToolError("invalid_input")
            self._handoff_scopes = tuple(handoffs.scopes)
        self._approvals = dict(approval_handoffs or {})
        if any(not handoffs or tool + "_start" not in self._starts or not isinstance(approval, PipelineTaskApproval)
               for tool, approval in self._approvals.items()):
            raise QueryToolError("invalid_input")
        self._manifest_resource_uri = PIPELINE_TASK_MANIFEST_URI if publish_manifest is True else None

    @property
    def manifest_resource_uri(self) -> str | None:
        return self._manifest_resource_uri

    @property
    def recovery_tools(self) -> dict[str, Any] | None:
        if self._recovery is None:
            return None
        return dict(copy.deepcopy(WIRE["task_recovery"]), scopes=list(self._recovery_scopes))

    @property
    def handoff_tools(self) -> dict[str, Any] | None:
        if self._handoffs is None:
            return None
        tools = dict(copy.deepcopy(WIRE["task_handoff"]), scopes=list(self._handoff_scopes))
        if not callable(getattr(self._handoffs.store, "scan", None)):
            del tools["list"]
        if not self._approvals:
            del tools["approval"]
            del tools["decide"]
        return tools

    def read_manifest(self, access: ToolkitAccess[A]) -> dict[str, Any]:
        if self._manifest_resource_uri is None:
            raise QueryToolError("not_found")
        entries = []
        for entry in self._toolkit.list_pipeline_contracts(access):
            tool = entry["tool"]
            if tool + "_start" not in self._starts:
                continue
            form = self._form_for(tool, access)
            params = form.request["params"] if form else None
            clarification = {"message": params["message"], "schema": params["requestedSchema"]} if params else None
            entries.append({**entry, "clarification": clarification})
        return {
            "contents": [
                {
                    "uri": self._manifest_resource_uri,
                    "mimeType": "application/json",
                    "text": _json(_build_manifest(entries)),
                }
            ]
        }

    def can_create(self, name: str) -> bool:
        return name in self._starts or (self._recovery is not None and name == WIRE["task_recovery"]["confirm"]["name"])

    def _form_for(self, tool: str, access: ToolkitAccess[A]) -> PipelineTaskInputForm[A] | None:
        form = self._forms.get(tool)
        return form if form is not None and all(scope in access.scopes for scope in form.scopes) else None

    def _owner(self, caller: PipelineTaskCaller[A]) -> str:
        fields = [caller.resource, caller.subject, caller.client_id]
        if (
            caller.access.principal is None
            or not caller.resource
            or not caller.subject
            or any(not isinstance(value, str) or len(value) > 2048 for value in fields)
        ):
            raise QueryToolError("not_found")
        digest = job_value_hash(["gregale.pipeline-task.v1", self._namespace, *fields])
        return hmac.new(self._key, digest.encode(), hashlib.sha256).hexdigest()

    def _readable(self, start: str, access: ToolkitAccess[A]) -> str:
        tool = self._starts.get(start)
        names = {entry["name"] for entry in self._toolkit.list_tools(access)}
        if not tool or start not in names or tool + "_status" not in names:
            raise QueryToolError("not_found")
        return tool

    def _bound(self, row: PipelineTaskBinding | None, lookup: _Lookup) -> PipelineTaskBinding:
        if row is None:
            raise QueryToolError("not_found")
        if (
            not _task_id(row["task_id"])
            or ("replaces_task_id" in row and (
                not _task_id(row["replaces_task_id"]) or row["replaces_task_id"] == row["task_id"]
            ))
            or row["tool_name"] not in self._starts
            or not _uuid(row["request_id"])
            or not _hash(row["receipt_identity"])
            or not _positive(row["ttl_ms"], _MAX_TTL)
            or any(row[key] != expected for key, expected in lookup.items())  # type: ignore[literal-required]
        ):
            raise QueryToolError("invalid_output")
        try:
            datetime.fromisoformat(row["created_at"])
        except (TypeError, ValueError):
            raise QueryToolError("invalid_output") from None
        return row

    def _frame(self, row: PipelineTaskBinding, message: str, sampled: str | None = None) -> _TaskFrame:
        return {
            "taskId": row["task_id"],
            "createdAt": row["created_at"],
            "lastUpdatedAt": sampled or row["created_at"],
            "ttlMs": row["ttl_ms"],
            "pollIntervalMs": self._poll_interval,
            "statusMessage": message,
        }

    async def _scoped(self, timeout: float, work: Callable[[Callable[[], float]], Awaitable[T]]) -> T:
        async def operation(check: Callable[[], None]) -> T:
            end = asyncio.get_running_loop().time() + timeout

            def left() -> float:
                check()
                remaining = end - asyncio.get_running_loop().time()
                if remaining <= 0:
                    raise QueryToolError("deadline")
                return remaining

            return await work(left)

        return await _query_scope(timeout, operation)

    async def create(
        self, name: str, args: Any, caller: PipelineTaskCaller[A], *, timeout: float
    ) -> PipelineTaskHandle | dict[str, Any]:
        if self._recovery is not None and name == WIRE["task_recovery"]["confirm"]["name"]:
            return await self._recover(args, caller, timeout, True)
        args = copy.deepcopy(args)
        request_id = args.get("request_id") if isinstance(args, dict) else None
        if not isinstance(request_id, str) or not _uuid(request_id):
            raise QueryToolError("invalid_input")
        lookup = _TaskRequest(tool_name=name, request_id=request_id)

        async def work(left: Callable[[], float]) -> PipelineTaskHandle | dict[str, Any]:
            owner, tool = self._owner(caller), self._readable(name, caller.access)
            previous = await self._store.load(self._namespace, owner, lookup)
            if previous:
                row = self._bound(previous, lookup)
                current = await self._toolkit.call(
                    tool + "_status", {"request_id": request_id}, caller.access, timeout=left()
                )
                if _receipt(current, tool, request_id)[1] != row["receipt_identity"]:
                    raise QueryToolError("not_found")
            started = await self._toolkit.call(name, args, caller.access, timeout=left())
            if started.get("isError"):
                return started
            view, identity, _run = _receipt(started, tool, request_id)
            if view["state"] not in ("started", "start_unknown"):
                raise QueryToolError("invalid_output")
            left()
            binding = await self._store.bind(
                self._namespace,
                owner,
                _NewBinding(
                    **lookup,
                    task_id=str(uuid.uuid4()),
                    receipt_identity=identity,
                    ttl_ms=self._ttl_ms,
                ),
            )
            row = self._bound(binding, lookup)
            if row["receipt_identity"] != identity:
                raise QueryToolError("not_found")
            return PipelineTaskHandle(**self._frame(row, "Pipeline Task accepted"), resultType="task", status="working")

        return await self._scoped(timeout, work)

    async def preview_recovery(self, args: Any, caller: PipelineTaskCaller[A], *, timeout: float) -> dict[str, Any]:
        result = await self._recover(args, caller, timeout, False)
        if "isError" not in result:
            raise QueryToolError("invalid_output")
        return cast(dict[str, Any], result)

    @staticmethod
    def _recovery_error(code: str) -> dict[str, Any]:
        return {"isError": True, "content": [{"type": "text", "text": _json({"code": code})}]}

    async def _recover(
        self, value: Any, caller: PipelineTaskCaller[A], timeout: float, confirm: bool
    ) -> PipelineTaskHandle | dict[str, Any]:
        recovery, store = self._recovery, self._linked_store
        if recovery is None or store is None:
            raise QueryToolError("not_found")
        args = _recovery_request(value, confirm)

        async def work(left: Callable[[], float]) -> PipelineTaskHandle | dict[str, Any]:
            owner = self._owner(caller)
            if not all(scope in caller.access.scopes for scope in self._recovery_scopes):
                return self._recovery_error("not_found")
            try:
                original, tool, _, content, identity, run_id = await self._read_task(args["task_id"], caller, left)
            except QueryToolError as error:
                if error.code == "not_found":
                    return self._recovery_error("not_found")
                raise
            if content["state"] != "failed" or content.get("source_failure") != "source_unavailable" or run_id is None:
                return self._recovery_error("recovery_blocked")
            entry = next((item for item in self._toolkit.list_pipeline_contracts(caller.access) if item["tool"] == tool), None)
            if entry is None or entry["contract_sha256"] != args["expected_contract_sha256"]:
                return self._recovery_error("revision_unavailable")
            contract_hash = entry["contract_sha256"]
            request_id = _recovery_request_id(self._key, owner, original["task_id"], args["recovery_request_id"])
            lookup = _TaskRequest(tool_name=tool + "_start", request_id=request_id)
            previous = await store.load_linked(self._namespace, owner, lookup)
            existing = await self._toolkit.call(tool + "_status", {"request_id": request_id}, caller.access, timeout=left())
            if existing.get("isError"):
                return self._recovery_error("not_found")

            async def link(receipt_identity: str, retained: PipelineTaskBinding | None = None) -> PipelineTaskHandle:
                if retained is None:
                    left()
                    retained = await store.bind_linked(self._namespace, owner, _NewLinkedBinding(
                        **lookup, task_id=str(uuid.uuid4()), receipt_identity=receipt_identity, ttl_ms=self._ttl_ms,
                        replaces_task_id=original["task_id"],
                    ))
                row = self._bound(retained, lookup)
                if row["receipt_identity"] != receipt_identity or row.get("replaces_task_id") != original["task_id"]:
                    raise QueryToolError("not_found")
                return _LinkedTaskHandle(
                    **self._frame(row, "Pipeline replacement accepted"), resultType="task", status="working",
                    _meta={"io.gregale/pipeline-recovery": _PipelineReplacement(
                        replaces_task_id=original["task_id"], request_id=request_id, tool=tool, contract_sha256=contract_hash,
                        recovery_request_id=args["recovery_request_id"],
                    )},
                )

            view = existing.get("structuredContent")
            if not isinstance(view, dict) or view.get("state") != "not_found":
                _, replacement, _ = _receipt(existing, tool, request_id)
                return await link(replacement, previous) if confirm else self._recovery_error("recovery_exists")
            if previous is not None:
                return self._recovery_error("not_found")
            if ("revision" in entry and entry["revision"]["starts"] == "retained") or "preview" not in entry:
                return self._recovery_error("recovery_blocked")
            prepared = await recovery.prepare(PipelineTaskRecoverySource(
                task_id=original["task_id"], tool=tool, request_id=original["request_id"],
                run_id=run_id, receipt_identity=identity, failure="source_unavailable",
            ), caller.access.principal, left())
            if not isinstance(prepared, dict) or "input" not in prepared or not _hash(prepared.get("previous_source_sha256")):
                return self._recovery_error("recovery_blocked")
            arguments = {"request_id": request_id, "input": prepared["input"], "expected_contract_sha256": contract_hash}
            if confirm:
                arguments["preview_token"] = args["preview_token"]
            result = await self._toolkit.call(
                lookup["tool_name"] if confirm else tool + "_preview", arguments, caller.access, timeout=left(),
            )
            if result.get("isError"):
                return result
            if not confirm:
                preview = result.get("structuredContent")
                token = preview.get("preview_token") if isinstance(preview, dict) else None
                if (not isinstance(preview, dict) or "summary" not in preview or not isinstance(token, str)
                    or not re.fullmatch(_TOKEN_PATTERN, token) or preview.get("expires_at") != int(token.split(".", 1)[0])):
                    raise QueryToolError("invalid_output")
                captured = {name: preview[name] for name in ("summary", "preview_token", "expires_at")} | {
                    "replaces_task_id": original["task_id"], "previous_source_sha256": prepared["previous_source_sha256"],
                }
                return {"isError": False, "structuredContent": captured, "content": [{"type": "text", "text": _json(captured)}]}
            started, receipt_identity, _ = _receipt(result, tool, request_id)
            if started["state"] not in ("started", "start_unknown"):
                raise QueryToolError("invalid_output")
            return await link(receipt_identity)

        return await self._scoped(timeout, work)

    async def grant_handoff(
        self, task_id: str, caller: PipelineTaskCaller[A], *, reviewer_subject: str, ttl_ms: int, timeout: float,
    ) -> PipelineTaskHandoff:
        settings = self._handoffs
        if (
            settings is None or not isinstance(reviewer_subject, str)
            or not 1 <= len(reviewer_subject) <= 2048 or not _positive(ttl_ms, _MAX_TTL)
        ):
            raise QueryToolError("invalid_input")

        async def work(left: Callable[[], float]) -> PipelineTaskHandoff:
            issuer = self._owner(caller)
            row, tool, _, content, _, run_id = await self._read_task(task_id, caller, left)
            pending = content.get("approval")
            approval = content["state"] == "awaiting_approval"
            provider = self._approvals.get(tool) if approval else self._forms.get(tool) if content["state"] == "awaiting_input" else None
            if (
                not provider or not run_id or not isinstance(pending, dict)
                or not _hash(pending.get("id")) or not isinstance(pending.get("expires_at"), str)
            ):
                raise QueryToolError("not_found")
            gate = await provider.inspect(pending["id"], caller.access.principal, timeout=left())
            if (
                gate is None or gate.kind != ("approval" if approval else "input") or gate.id != pending["id"] or gate.run_id != run_id
                or gate.decision is not None or not _hash(gate.binding_sha256)
            ):
                raise QueryToolError("not_found")
            now = datetime.now(UTC)
            try:
                expires = min(
                    now + timedelta(milliseconds=ttl_ms),
                    datetime.fromisoformat(row["created_at"]) + timedelta(milliseconds=row["ttl_ms"]),
                    datetime.fromisoformat(pending["expires_at"]),
                )
            except (ValueError, TypeError, OverflowError):
                raise QueryToolError("not_found") from None
            if expires <= now:
                raise QueryToolError("not_found")
            intent = PipelineTaskHandoffRecord(
                id=str(uuid.uuid4()), issuer_hash=issuer,
                reviewer_hash=_reviewer(self._key, self._namespace, caller.resource, reviewer_subject),
                task_id=task_id, tool=tool, run_id=run_id, gate_id=gate.id, binding_sha256=gate.binding_sha256,
                expires_at=expires.isoformat(timespec="milliseconds").replace("+00:00", "Z"),
            )
            left()
            saved = _parse_handoff(await settings.store.grant(self._namespace, copy.deepcopy(intent)))
            if saved != intent:
                raise QueryToolError("invalid_output")
            return PipelineTaskHandoff(handoff_id=saved["id"], expires_at=saved["expires_at"])

        return await self._scoped(timeout, work)

    async def revoke_handoff(self, id: str, caller: PipelineTaskCaller[A], *, timeout: float) -> None:
        settings = self._handoffs
        if settings is None or not _task_id(id):
            raise QueryToolError("not_found")

        async def work(left: Callable[[], float]) -> None:
            left()
            if not await settings.store.revoke(self._namespace, id, self._owner(caller)):
                raise QueryToolError("not_found")

        await self._scoped(timeout, work)

    def _handoff_reviewer(self, caller: PipelineTaskCaller[A]) -> str:
        self._owner(caller)
        if not all(scope in caller.access.scopes for scope in self._handoff_scopes):
            raise QueryToolError("not_found")
        return _reviewer(self._key, self._namespace, caller.resource, caller.subject)

    async def _active_handoff(
        self, id: str, reviewer: str, left: Callable[[], float]
    ) -> PipelineTaskHandoffRecord | None:
        assert self._handoffs is not None
        left()
        raw = await self._handoffs.store.load(self._namespace, id, reviewer)
        if raw is None:
            return None
        row = _parse_handoff(raw)
        if (row["id"] != id or row["reviewer_hash"] != reviewer
            or datetime.fromisoformat(row["expires_at"]) <= datetime.now(UTC)):
            return None
        return row

    async def _handoff_gate(
        self, row: PipelineTaskHandoffRecord, caller: PipelineTaskCaller[A], left: Callable[[], float], *, retry: bool,
    ) -> tuple[_Gate, PipelineTaskInputForm[A]] | None:
        form = self._form_for(row["tool"], caller.access)
        if form is None:
            return None
        gate = await form.inspect(row["gate_id"], caller.access.principal, timeout=left())
        if (
            gate is None or gate.kind != "input" or gate.id != row["gate_id"] or gate.run_id != row["run_id"]
            or gate.binding_sha256 != row["binding_sha256"]
            or (not gate.can_decide and not (retry and gate.decision == "submitted"))
        ):
            return None
        return gate, form

    async def _approval_gate(
        self, row: PipelineTaskHandoffRecord, caller: PipelineTaskCaller[A], left: Callable[[], float], *, retry: bool,
    ) -> tuple[PipelineGateView[Any, Any], PipelineTaskApproval[Any, Any, A]] | None:
        approval = self._approvals.get(row["tool"])
        if approval is None or not all(scope in caller.access.scopes for scope in approval.scopes):
            return None
        gate = await approval.inspect(row["gate_id"], caller.access.principal, timeout=left())
        if (
            gate is None or gate.kind != "approval" or gate.id != row["gate_id"] or gate.run_id != row["run_id"]
            or gate.binding_sha256 != row["binding_sha256"]
            or not (gate.can_decide or (retry and gate.decision and gate.decision.state in ("approved", "rejected")))
        ):
            return None
        return gate, approval

    async def _call_approval(self, name: str, args: Any, caller: PipelineTaskCaller[A], timeout: float) -> dict[str, Any]:
        decide = name == WIRE["task_handoff"]["decide"]["name"]
        expected = {"handoff_id", "state", "input", "decision_key"} if decide else {"handoff_id"}
        if (not isinstance(args, dict) or set(args) != expected or not _task_id(args["handoff_id"])
            or (decide and (not _hash(args["decision_key"]) or args["state"] not in ("approved", "rejected")))):
            raise QueryToolError("invalid_input")
        id = args["handoff_id"]

        async def work(left: Callable[[], float]) -> dict[str, Any]:
            reviewer = self._handoff_reviewer(caller)
            row = await self._active_handoff(id, reviewer, left)
            opened = await self._approval_gate(row, caller, left, retry=decide) if row is not None else None
            if row is None or opened is None:
                raise QueryToolError("not_found")
            gate, approval = opened
            review_digest = job_value_hash(gate.review)
            key = _approval_key(self._key, row, review_digest)
            content: dict[str, Any]
            if decide:
                if not hmac.compare_digest(args["decision_key"], key):
                    raise QueryToolError("invalid_input")
                state: PipelineTaskApprovalChoice = args["state"]
                decided_at = await approval.decide(gate.id, gate.binding_sha256, state, args["input"], caller.access.principal, {
                    "namespace": self._namespace, "id": id, "reviewer_hash": reviewer, "run_id": row["run_id"],
                }, timeout=left())
                content = {"handoff_id": id, "state": state, "decided_at": decided_at}
            else:
                if await self._active_handoff(id, reviewer, left) != row:
                    raise QueryToolError("not_found")
                view = PipelineTaskApprovalView(
                    handoff_id=id, expires_at=row["expires_at"], title=approval.title, review=gate.review,
                    review_sha256=review_digest, contract_sha256=approval.contract_sha256, decision_key=key,
                )
                content = dict(view)
            return {"isError": False, "structuredContent": content, "content": [{"type": "text", "text": _json(content)}]}

        return await self._scoped(timeout, work)

    async def _list_handoffs(self, args: Any, caller: PipelineTaskCaller[A], timeout: float) -> PipelineTaskHandoffPage:
        settings = self._handoffs
        scan = getattr(settings.store, "scan", None) if settings is not None else None
        if not callable(scan):
            raise QueryToolError("not_found")
        if (not isinstance(args, dict) or set(args) - {"cursor", "page_size", "include_approvals"}
            or type(args.get("include_approvals", False)) is not bool):
            raise QueryToolError("invalid_input")
        size = args.get("page_size", 20)
        if not _positive(size, _PAGE_LIMIT):
            raise QueryToolError("invalid_input")

        async def work(left: Callable[[], float]) -> PipelineTaskHandoffPage:
            reviewer = self._handoff_reviewer(caller)
            include_approvals = args.get("include_approvals", False)
            cursor_binding = reviewer + ":approval" if include_approvals else reviewer
            after = _after(self._key, cursor_binding, args["cursor"]) if "cursor" in args else ""
            left()
            rows = await scan(self._namespace, reviewer, after, _SCAN_LIMIT + 1)
            if len(rows) > _SCAN_LIMIT + 1:
                raise QueryToolError("invalid_output")
            items: list[PipelineTaskHandoffSummary] = []
            scanned = 0
            for candidate in rows[:_SCAN_LIMIT]:
                position, row = candidate["position"], _parse_handoff(candidate["grant"])
                if not _POSITION.fullmatch(position) or position <= after or row["reviewer_hash"] != reviewer:
                    raise QueryToolError("invalid_output")
                after = position
                scanned += 1
                opened = await self._handoff_gate(row, caller, left, retry=False)
                approved = await self._approval_gate(row, caller, left, retry=False) if opened is None and include_approvals else None
                if (opened is None and approved is None) or await self._active_handoff(row["id"], reviewer, left) != row:
                    continue
                item = PipelineTaskHandoffSummary(handoff_id=row["id"], expires_at=row["expires_at"], question="")
                if opened is not None:
                    item["question"] = opened[1].request["params"]["message"]
                elif approved is not None:
                    item["question"], item["kind"] = approved[1].title, "approval"
                items.append(item)
                if len(items) == size:
                    break
            return PipelineTaskHandoffPage(items=items, next_cursor=_cursor(self._key, cursor_binding, after) if scanned < len(rows) else None)

        return await self._scoped(timeout, work)

    async def call_handoff(self, name: str, args: Any, caller: PipelineTaskCaller[A], *, timeout: float) -> dict[str, Any]:
        settings = self._handoffs
        tools = WIRE["task_handoff"]
        if name == tools["list"]["name"]:
            page = await self._list_handoffs(copy.deepcopy(args), caller, timeout)
            return {"isError": False, "structuredContent": page, "content": [{"type": "text", "text": _json(page)}]}
        if self._approvals and name in (tools["approval"]["name"], tools["decide"]["name"]):
            return await self._call_approval(name, copy.deepcopy(args), caller, timeout)
        submit = name == tools["reply"]["name"]
        if settings is None or (not submit and name != tools["read"]["name"]):
            raise QueryToolError("not_found")
        args = copy.deepcopy(args)
        expected = {"handoff_id", "request_key", "input"} if submit else {"handoff_id"}
        if not isinstance(args, dict) or set(args) != expected or not _task_id(args["handoff_id"]):
            raise QueryToolError("invalid_input")
        id = args["handoff_id"]

        async def work(left: Callable[[], float]) -> dict[str, Any]:
            reviewer = self._handoff_reviewer(caller)
            row = await self._active_handoff(id, reviewer, left)
            opened = await self._handoff_gate(row, caller, left, retry=submit) if row is not None else None
            if row is None or opened is None:
                raise QueryToolError("not_found")
            gate, form = opened
            anchor = (row["task_id"], row["gate_id"], row["run_id"], row["id"])
            if submit:
                if not isinstance(args["request_key"], str):
                    raise QueryToolError("invalid_input")
                gate_id, binding, value = _parse_form_reply(self._key, anchor, {
                    args["request_key"]: {"action": "accept", "content": args["input"]},
                })
                if gate_id != gate.id or binding != gate.binding_sha256:
                    raise QueryToolError("invalid_input")
                await form.submit_handoff(gate_id, binding, value, caller.access.principal, {
                    "namespace": self._namespace, "id": id, "reviewer_hash": reviewer, "run_id": row["run_id"],
                }, timeout=left())
            elif await self._active_handoff(id, reviewer, left) != row:
                raise QueryToolError("not_found")
            content = {"handoff_id": id, "submitted": True} if submit else {
                "handoff_id": id, "expires_at": row["expires_at"], "form": form.request,
                "request_key": _form_key(self._key, anchor, gate.id, gate.binding_sha256),
            }
            return {"isError": False, "structuredContent": content, "content": [{"type": "text", "text": _json(content)}]}

        return await self._scoped(timeout, work)

    async def _read_task(
        self, task_id: str, caller: PipelineTaskCaller[A], left: Callable[[], float]
    ) -> tuple[PipelineTaskBinding, str, dict[str, str], dict[str, Any], str, str | None]:
        if not _task_id(task_id):
            raise QueryToolError("not_found")
        owner, lookup = self._owner(caller), _TaskID(task_id=task_id)
        load = self._linked_store.load_linked if self._linked_store is not None else self._store.load
        row = self._bound(await load(self._namespace, owner, lookup), lookup)
        tool = self._readable(row["tool_name"], caller.access)
        args = {"request_id": row["request_id"]}
        result = await self._toolkit.call(tool + "_status", args, caller.access, timeout=left())
        content, identity, run_id = _receipt(result, tool, row["request_id"])
        if identity != row["receipt_identity"]:
            raise QueryToolError("not_found")
        return row, tool, args, content, identity, run_id

    async def get(self, task_id: str, caller: PipelineTaskCaller[A], *, timeout: float) -> PipelineTaskSnapshot:
        async def work(left: Callable[[], float]) -> PipelineTaskSnapshot:
            row, tool, args, content, identity, run_id = await self._read_task(task_id, caller, left)
            state, raw = content["state"], content.get("steps")
            if state not in _STATES or not isinstance(raw, list) or len(raw) > 1000:
                raise QueryToolError("invalid_output")
            steps = []
            for step in raw:
                if (
                    not isinstance(step, dict)
                    or not isinstance(step.get("name"), str)
                    or len(step["name"]) > 128
                    or not isinstance(step.get("status"), str)
                    or len(step["status"]) > 32
                    or type(step.get("attempt")) is not int
                    or step["attempt"] < 0
                ):
                    raise QueryToolError("invalid_output")
                steps.append(_Step(name=step["name"], status=step["status"], attempt=step["attempt"]))
            progress = PipelineTaskProgress(request_id=row["request_id"], run_id=run_id, state=state, steps=steps)
            if "replaces_task_id" in row:
                progress["replaces_task_id"] = row["replaces_task_id"]
            if "source_failure" in content:
                failure = content["source_failure"]
                if state != "failed" or not _is_pinned_source_failure(failure):
                    raise QueryToolError("invalid_output")
                progress["source_failure"] = failure
            message = "Pipeline " + state.replace("_", " ")
            if state in ("awaiting_approval", "awaiting_input"):
                try:
                    approval = content["approval"]
                    review = {key: approval[key] for key in ("kind", "id", "expires_at", "review_url")}
                    if not _uuid(run_id):
                        raise ValueError
                    _check_review({"review": review, "run_id": run_id, "state": state})  # type: ignore[typeddict-item]
                except (KeyError, TypeError, ValueError):
                    raise QueryToolError("invalid_output") from None
                progress["review"] = review  # type: ignore[typeddict-item]
                message += " · " + review["review_url"]
            if state == "start_unknown":
                message = "Pipeline start uncertain. Resolve the existing receipt before retrying."
                try:
                    follow = await self._toolkit.resolve_work_inbox_follow_up(
                        {"kind": "pipeline", "name": tool, "request_id": row["request_id"]},
                        caller.access,
                        timeout=left(),
                    )
                    if follow["kind"] == "tool_call" and follow["name"] == tool + "_reconcile":
                        progress["follow_up"] = follow
                except (QueryToolError, ApplicationToolkitError) as error:
                    if error.code not in ("not_found", "tool_not_found"):
                        raise
            sampled = datetime.now(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")
            snapshot = cast(
                PipelineTaskSnapshot,
                {
                    **self._frame(row, message, sampled),
                    "resultType": "complete",
                    "status": "working",
                    "_meta": {"io.gregale/pipeline": progress},
                },
            )
            form = self._form_for(tool, caller.access) if state == "awaiting_input" and caller.supports_forms else None
            if form and run_id and "review" in progress:
                gate_id = progress["review"]["id"]
                gate = await form.inspect(gate_id, caller.access.principal, timeout=left())
                if (
                    gate
                    and gate.kind == "input"
                    and gate.id == gate_id
                    and gate.run_id == run_id
                    and gate.can_decide
                    and gate.decision is None
                    and _hash(gate.binding_sha256)
                ):
                    key = _form_key(self._key, (task_id, identity, run_id), gate.id, gate.binding_sha256)
                    snapshot["status"] = "input_required"
                    snapshot["inputRequests"] = {key: form.request}
                    return snapshot
            if state == "succeeded":
                if not any(entry["name"] == tool + "_result" for entry in self._toolkit.list_tools(caller.access)):
                    raise QueryToolError("not_found")
                output = await self._toolkit.call(tool + "_result", args, caller.access, timeout=left())
                view, current_identity, current_run = _receipt(output, tool, row["request_id"])
                if (
                    current_identity != identity
                    or current_run != run_id
                    or view["state"] != "succeeded"
                    or "output" not in view
                ):
                    raise QueryToolError("invalid_output")
                snapshot["status"] = "completed"
                snapshot["result"] = {**output, "resultType": "complete"}
            elif state == "failed":
                snapshot["status"] = "failed"
                snapshot["error"] = {"code": -32603, "message": "Pipeline failed"}
            elif state == "cancelled":
                snapshot["status"] = "cancelled"
            return snapshot

        return await self._scoped(timeout, work)

    async def update(self, task_id: str, responses: Any, caller: PipelineTaskCaller[A], *, timeout: float) -> None:
        # Capture the request before awaiting any authorization or store reads.
        responses = copy.deepcopy(responses)

        async def work(left: Callable[[], float]) -> None:
            _, tool, _, content, identity, run_id = await self._read_task(task_id, caller, left)
            if tool not in self._forms:
                raise QueryToolError("invalid_input")
            form = self._form_for(tool, caller.access)
            if form is None:
                raise QueryToolError("not_found")
            if not run_id:
                raise QueryToolError("invalid_input")
            gate_id, binding, value = _parse_form_reply(self._key, (task_id, identity, run_id), responses)
            gate = await form.inspect(gate_id, caller.access.principal, timeout=left())
            if (
                gate is None
                or gate.kind != "input"
                or gate.id != gate_id
                or gate.run_id != run_id
                or gate.binding_sha256 != binding
            ):
                raise QueryToolError("invalid_input")
            pending = content.get("approval")
            if gate.decision != "submitted" and (
                gate.decision is not None
                or not gate.can_decide
                or content["state"] != "awaiting_input"
                or not isinstance(pending, dict)
                or pending.get("id") != gate_id
            ):
                raise QueryToolError("invalid_input")
            await form.submit(gate_id, binding, value, caller.access.principal, timeout=left())

        await self._scoped(timeout, work)
