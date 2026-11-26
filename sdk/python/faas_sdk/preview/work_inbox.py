"""Read-only discovery over existing SDK receipts, with fresh adapter inspection."""

from __future__ import annotations

import hashlib
import re
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import asdict, dataclass
from typing import Any, Generic, Literal, NotRequired, Protocol, TypedDict, TypeVar

from ..job_runtime import JobContract, job_input_id, job_value_hash
from ._work_inbox_protocol import FOLLOW_UP_SPEC, SPEC, SQL
from .event_pipeline_state import event_pipeline_scope
from .outbound_action_state import OutboundActionError, outbound_action_identity
from .outbound_actions import OutboundAction
from .pipeline_state import PipelineScope, _hash, _uuid, pipeline_name
from .pipeline_tool_state import PipelineToolError, tool_name
from .pipeline_tool_state import identity as pipeline_tool_identity
from .pipeline_tools import PipelineTool, PipelineToolStartMatch
from .query_tools import QueryActor, QueryAuthorizedLoadContext, QueryLoadPage, QueryTool, QueryToolError, _json

A = TypeVar("A")
I = TypeVar("I")
O = TypeVar("O")
WorkInboxKind = Literal["pipeline", "action"]
WorkInboxState = Literal[
    "start_unknown",
    "running",
    "awaiting_approval",
    "awaiting_input",
    "succeeded",
    "failed",
    "cancelled",
    "prepared",
    "unknown",
    "pending",
    "needs_review",
]
WorkInboxNextStep = Literal[
    "reconcile_start", "open_review", "inspect", "read_result", "reconcile_action", "review_action", "none"
]


class WorkInboxFilter(TypedDict, total=False):
    kind: WorkInboxKind
    state: WorkInboxState
    business_ref: str


class WorkInboxReview(TypedDict):
    kind: Literal["approval", "input"]
    id: str
    expires_at: str
    review_url: str


class WorkInboxEntry(TypedDict):
    kind: WorkInboxKind
    name: str
    state: WorkInboxState
    next_step: WorkInboxNextStep
    request_id: str
    business_ref: NotRequired[str]
    run_id: NotRequired[str]
    action_id: NotRequired[str]
    review: NotRequired[WorkInboxReview]


class WorkInboxReference(TypedDict):
    kind: WorkInboxKind
    name: str
    request_id: str
    business_ref: NotRequired[str]


class _MatchedRun(TypedDict):
    run_id: str


class WorkInbox(QueryTool[WorkInboxFilter, WorkInboxEntry, A]):
    """Current inbox reads, including an exact receipt lookup."""

    def __init__(
        self,
        *,
        exact: QueryTool[WorkInboxReference, WorkInboxEntry, A],
        accepted: QueryTool[WorkInboxReference, _MatchedRun, A],
        **options: Any,
    ) -> None:
        super().__init__(JobContract(SPEC), **options)
        self._exact, self._accepted = exact, accepted

    @staticmethod
    def _reference_fields(reference: WorkInboxReference) -> WorkInboxReference:
        if not isinstance(reference, dict) or not {"kind", "name", "request_id"} <= reference.keys():
            raise QueryToolError("invalid_input")
        value = WorkInboxReference(kind=reference["kind"], name=reference["name"], request_id=reference["request_id"])
        if "business_ref" in reference:
            value["business_ref"] = reference["business_ref"]
        return value

    async def find_entry(self, reference: WorkInboxReference, access: A, *, timeout: float) -> WorkInboxEntry | None:
        page = await self._exact.page(self._reference_fields(reference), access, timeout=timeout)
        return page["items"][0] if page["items"] else None

    async def find_accepted_run(self, reference: WorkInboxReference, access: A, *, timeout: float) -> str | None:
        page = await self._accepted.page(self._reference_fields(reference), access, timeout=timeout)
        return page["items"][0]["run_id"] if page["items"] else None


@dataclass(frozen=True, slots=True)
class WorkInboxCandidate:
    kind: WorkInboxKind
    name: str
    identity: str
    request_id: str
    position: str


@dataclass(frozen=True, slots=True)
class WorkInboxSelector:
    kind: WorkInboxKind
    name: str
    contract_sha256: str | None = None
    receipt_subject_id: str | None = None


@dataclass(frozen=True, slots=True)
class WorkInboxScan:
    scope: PipelineScope
    actor: QueryActor
    selectors: Sequence[WorkInboxSelector]
    after: str | None
    limit: int


@dataclass(frozen=True, slots=True)
class WorkInboxExactLookup:
    scope: PipelineScope
    actor: QueryActor
    selector: WorkInboxSelector
    identity: str
    request_id: str


class WorkInboxStore(Protocol):
    async def scan(
        self, request: WorkInboxScan, check_cancelled: Callable[[], None]
    ) -> Sequence[WorkInboxCandidate]: ...


@dataclass(frozen=True, slots=True)
class WorkInboxSource(Generic[A]):
    selector: WorkInboxSelector
    inspect: Callable[[WorkInboxCandidate, QueryActor, PipelineScope, A, float], Awaitable[WorkInboxEntry | None]]
    entry_name: str | None = None
    find_accepted_run: Callable[[str, A, float], Awaitable[PipelineToolStartMatch]] | None = None


def work_inbox_position(kind: WorkInboxKind, identity: str) -> str:
    if not _hash(identity) or kind not in ("pipeline", "action"):
        raise QueryToolError("invalid_output")
    return hashlib.sha256(bytes.fromhex(identity)).hexdigest() + ("p" if kind == "pipeline" else "a")


class PostgresWorkInboxStore:
    def __init__(self, query: Callable[[str, list[object]], Awaitable[list[dict[str, str]]]]):
        self._query = query

    async def has_receipt(self, request: WorkInboxExactLookup, check_cancelled: Callable[[], None]) -> bool:
        check_cancelled()
        selector = request.selector
        args: list[object] = [
            request.identity,
            _json(request.scope.to_dict()),
            request.actor.owner_id,
            selector.name,
            request.request_id,
        ]
        if selector.kind == "pipeline":
            args.extend((selector.receipt_subject_id or request.actor.subject_id, selector.contract_sha256))
        rows = await self._query(SQL[selector.kind + "_exact"], args)
        check_cancelled()
        if len(rows) > 1:
            raise QueryToolError("invalid_output")
        return bool(rows)

    async def scan(self, request: WorkInboxScan, check_cancelled: Callable[[], None]) -> Sequence[WorkInboxCandidate]:
        candidates: list[WorkInboxCandidate] = []
        for kind in ("pipeline", "action"):
            descriptors = [
                dict(name=s.name, contract_sha256=s.contract_sha256, receipt_subject_id=s.receipt_subject_id)
                for s in request.selectors
                if s.kind == kind
            ]
            if not descriptors:
                continue
            check_cancelled()
            rows = await self._query(
                SQL[kind],
                [
                    _json(descriptors),
                    _json(request.scope.to_dict()),
                    request.actor.owner_id,
                    request.actor.subject_id,
                    request.after or "",
                    request.limit,
                ],
            )
            check_cancelled()
            if len(rows) > request.limit:
                raise QueryToolError("invalid_output")
            for row in rows:
                if set(row) != {"kind", "name", "identity", "request_id", "position"} or row["kind"] != kind:
                    raise QueryToolError("invalid_output")
                candidates.append(
                    WorkInboxCandidate(kind, row["name"], row["identity"], row["request_id"], row["position"])
                )
        return sorted(candidates, key=lambda c: c.position)[: request.limit]


def pipeline_work_inbox_source(
    tool: PipelineTool[I, O, A], *, receipt_subject_id: str | None = None
) -> WorkInboxSource[A]:
    name, digest = tool.name, tool.contract.sha256

    async def inspect(
        candidate: WorkInboxCandidate, actor: QueryActor, scope: PipelineScope, access: A, timeout: float
    ) -> WorkInboxEntry | None:
        status = await tool.inspect(candidate.request_id, access, timeout=timeout)
        if status is None:
            return None
        intent, checkpoint = status.receipt.intent, status.receipt.checkpoint
        if (
            intent["identity"] != candidate.identity
            or intent["name"] != name
            or intent["contract_sha256"] != digest
            or intent["request_id"] != candidate.request_id
            or intent["owner_id"] != actor.owner_id
            or intent["subject_id"] != (receipt_subject_id or actor.subject_id)
            or intent["scope"] != scope.to_dict()
        ):
            return None
        next_step: WorkInboxNextStep = "none"
        if status.approval:
            next_step = "open_review"
        elif status.state == "succeeded":
            next_step = "read_result"
        elif status.state == "start_unknown":
            next_step = "reconcile_start"
        elif status.state == "running":
            next_step = "inspect"
        result = WorkInboxEntry(
            kind="pipeline", name=name, state=status.state, next_step=next_step, request_id=candidate.request_id
        )
        if checkpoint:
            result["run_id"] = checkpoint.run_id
        if status.approval:
            a = status.approval
            result["review"] = WorkInboxReview(kind=a.kind, id=a.id, expires_at=a.expires_at, review_url=a.review_url)
        return result

    async def find_accepted_run(request_id: str, access: A, timeout: float) -> PipelineToolStartMatch:
        return await tool.find_accepted_run(request_id, access, timeout=timeout)

    return WorkInboxSource(
        WorkInboxSelector("pipeline", name, digest, receipt_subject_id), inspect, find_accepted_run=find_accepted_run
    )


def outbound_work_inbox_source(action: OutboundAction[I, O, A]) -> WorkInboxSource[A]:
    name = action.tool.name
    if not name.startswith("action_"):
        raise QueryToolError("invalid_input")

    async def inspect(
        candidate: WorkInboxCandidate, actor: QueryActor, scope: PipelineScope, access: A, timeout: float
    ) -> WorkInboxEntry | None:
        view = await action.inspect(candidate.identity, access, timeout=timeout)
        if view["action_id"] != candidate.identity:
            return None
        state = view["outcome"]["state"]
        next_step: WorkInboxNextStep = "none"
        if state in ("unknown", "pending"):
            next_step = "reconcile_action"
        elif state == "needs_review":
            next_step = "review_action"
        elif state == "succeeded":
            next_step = "read_result"
        elif state == "prepared":
            next_step = "inspect"
        return WorkInboxEntry(
            kind="action",
            name=name,
            state=state,
            next_step=next_step,
            request_id=candidate.request_id,
            action_id=view["action_id"],
        )

    return WorkInboxSource(WorkInboxSelector("action", name.removeprefix("action_")), inspect, name)


def create_work_inbox(
    *,
    scope: PipelineScope,
    cursor_key: str,
    sources: Sequence[WorkInboxSource[A]],
    store: WorkInboxStore,
    authorize: Callable[[WorkInboxFilter, A, Callable[[], None]], Awaitable[QueryActor | None]],
    business_reference: Callable[[WorkInboxCandidate, QueryActor, Callable[[], None]], Awaitable[str | None]]
    | None = None,
    scan_limit: int = 24,
) -> WorkInbox[A]:
    scope = event_pipeline_scope(scope)
    if type(scan_limit) is not int or not 1 <= scan_limit <= 100 or not 1 <= len(sources) <= 32:
        raise QueryToolError("invalid_input")
    by_name: dict[tuple[WorkInboxKind, str], WorkInboxSource[A]] = {}
    for source in sources:
        selector = source.selector
        key = (selector.kind, selector.name)
        if (
            selector.kind not in ("pipeline", "action")
            or not tool_name(selector.name)
            or key in by_name
            or (
                not _hash(selector.contract_sha256)
                or selector.receipt_subject_id is not None
                and not pipeline_name(selector.receipt_subject_id)
                if selector.kind == "pipeline"
                else selector.contract_sha256 is not None or selector.receipt_subject_id is not None
            )
            or not callable(source.inspect)
        ):
            raise QueryToolError("invalid_input")
        by_name[key] = source
    selectors = tuple(by_name[key].selector for key in sorted(by_name))
    revision = job_value_hash(
        [
            [{k: v for k, v in asdict(selector).items() if v is not None} for selector in selectors],
            scan_limit,
            "work-inbox-v1",
        ]
    )

    async def authorized(input: WorkInboxFilter, access: A, check: Callable[[], None]) -> QueryActor | None:
        if "business_ref" in input and (business_reference is None or not job_input_id(input["business_ref"])):
            raise QueryToolError("invalid_input")
        return await authorize(input, access, check)

    def receipt_identity(selector: WorkInboxSelector, actor: QueryActor, request_id: str) -> str:
        if selector.kind == "action":
            return outbound_action_identity(scope, selector.name, request_id)
        return pipeline_tool_identity(scope, selector.name, selector.receipt_subject_id or actor.subject_id, request_id)

    async def inspect(
        source: WorkInboxSource[A],
        candidate: WorkInboxCandidate,
        input: WorkInboxFilter,
        context: QueryAuthorizedLoadContext,
        access: A,
    ) -> WorkInboxEntry | None:
        try:
            item = await source.inspect(candidate, context.actor, scope, access, context.remaining())
        except PipelineToolError as error:
            if error.code == "unauthorized":
                return None
            if error.code == "deadline":
                raise QueryToolError("deadline") from None
            raise
        except OutboundActionError as error:
            if error.code in ("unauthorized", "not_found", "conflict"):
                return None
            if error.code == "deadline":
                raise QueryToolError("deadline") from None
            raise
        context.check_cancelled()
        if item is None or ("state" in input and item["state"] != input["state"]):
            return None
        reference = (
            await business_reference(candidate, context.actor, context.check_cancelled) if business_reference else None
        )
        context.check_cancelled()
        if reference is not None and not job_input_id(reference):
            raise QueryToolError("invalid_output")
        if "business_ref" in input and input["business_ref"] != reference:
            return None
        return item if reference is None else {**item, "business_ref": reference}

    async def load(
        input: WorkInboxFilter, context: QueryAuthorizedLoadContext, access: A
    ) -> QueryLoadPage[WorkInboxEntry]:
        selected = tuple(s for s in selectors if "kind" not in input or s.kind == input["kind"])
        rows = await store.scan(
            WorkInboxScan(scope, context.actor, selected, context.after, scan_limit + 1), context.check_cancelled
        )
        context.check_cancelled()
        if len(rows) > scan_limit + 1:
            raise QueryToolError("invalid_output")
        items: list[WorkInboxEntry] = []
        position, consumed = context.after or "", 0
        for candidate in rows:
            if consumed >= scan_limit or len(items) >= context.limit:
                break
            source = by_name.get((candidate.kind, candidate.name))
            if (
                source is None
                or source.selector not in selected
                or not _uuid(candidate.request_id)
                or work_inbox_position(candidate.kind, candidate.identity) != candidate.position
                or candidate.position <= position
            ):
                raise QueryToolError("invalid_output")
            if candidate.identity != receipt_identity(source.selector, context.actor, candidate.request_id):
                raise QueryToolError("invalid_output")
            position = candidate.position
            consumed += 1
            item = await inspect(source, candidate, input, context, access)
            if item is not None:
                items.append(item)
        return QueryLoadPage(items=items, after=position if consumed < len(rows) else None)

    def filters(reference: WorkInboxReference) -> WorkInboxFilter:
        value = WorkInboxFilter(kind=reference["kind"])
        if "business_ref" in reference:
            value["business_ref"] = reference["business_ref"]
        return value

    async def authorize_exact(reference: WorkInboxReference, access: A, check: Callable[[], None]) -> QueryActor | None:
        if not _uuid(reference["request_id"]) or not re.fullmatch(r"[a-z][a-z0-9_]{0,63}", reference["name"]):
            raise QueryToolError("invalid_input")
        return await authorized(filters(reference), access, check)

    async def lookup_entry(
        reference: WorkInboxReference, context: QueryAuthorizedLoadContext, access: A
    ) -> tuple[WorkInboxEntry, WorkInboxSource[A]] | None:
        kind, name, request_id = reference["kind"], reference["name"], reference["request_id"]
        matches = [s for s in by_name.values() if s.selector.kind == kind and (s.entry_name or s.selector.name) == name]
        if len(matches) != 1:
            return None
        source = matches[0]
        has_receipt = getattr(store, "has_receipt", None)
        if not callable(has_receipt):
            raise QueryToolError("query_failed")
        selector = source.selector
        identity = receipt_identity(selector, context.actor, request_id)
        found = await has_receipt(
            WorkInboxExactLookup(scope, context.actor, selector, identity, request_id), context.check_cancelled
        )
        context.check_cancelled()
        if not found:
            return None
        candidate = WorkInboxCandidate(kind, selector.name, identity, request_id, work_inbox_position(kind, identity))
        item = await inspect(source, candidate, filters(reference), context, access)
        if item is None:
            return None
        if (
            item["kind"] != kind
            or item["name"] != name
            or item["request_id"] != request_id
            or (kind == "action" and item.get("action_id") != identity)
        ):
            raise QueryToolError("invalid_output")
        return item, source

    async def load_exact(
        reference: WorkInboxReference, context: QueryAuthorizedLoadContext, access: A
    ) -> QueryLoadPage[WorkInboxEntry]:
        entry = await lookup_entry(reference, context, access)
        return QueryLoadPage(items=[entry[0]] if entry else [], after=None)

    async def load_accepted(
        reference: WorkInboxReference, context: QueryAuthorizedLoadContext, access: A
    ) -> QueryLoadPage[_MatchedRun]:
        entry = await lookup_entry(reference, context, access)
        if entry is None:
            return QueryLoadPage(items=[], after=None)
        item, source = entry
        find = source.find_accepted_run
        if item["state"] != "start_unknown" or source.selector.kind != "pipeline" or find is None:
            return QueryLoadPage(items=[], after=None)
        try:
            outcome = await find(reference["request_id"], access, context.remaining())
        except PipelineToolError as error:
            if error.code == "unauthorized":
                raise QueryToolError("unauthorized") from None
            if error.code == "deadline":
                raise QueryToolError("deadline") from None
            if error.code == "receipt_conflict":
                raise QueryToolError("invalid_output") from None
            raise
        context.check_cancelled()
        if outcome["request_id"] != reference["request_id"]:
            raise QueryToolError("invalid_output")
        if outcome["state"] == "start_unknown":
            return QueryLoadPage(items=[], after=None)
        if outcome["state"] != "found" or not _uuid(outcome.get("run_id")):
            raise QueryToolError("invalid_output")
        return QueryLoadPage(items=[_MatchedRun(run_id=outcome["run_id"])], after=None)

    exact: QueryTool[WorkInboxReference, WorkInboxEntry, A] = QueryTool(
        JobContract({**SPEC, "name": "work-inbox-entry", "input": FOLLOW_UP_SPEC["input"]}),
        name="work_inbox_entry",
        description="Read one exact inbox entry by its receipt reference.",
        revision="work-inbox-entry-v1",
        scope=scope,
        cursor_key=cursor_key,
        max_page_size=1,
        authorize=authorize_exact,
        load_authorized=load_exact,
    )
    accepted: QueryTool[WorkInboxReference, _MatchedRun, A] = QueryTool(
        JobContract(
            {
                **SPEC,
                "name": "work-inbox-start",
                "input": FOLLOW_UP_SPEC["input"],
                "output": {
                    "type": "object",
                    "additionalProperties": False,
                    "required": ["run_id"],
                    "properties": {"run_id": SPEC["output"]["properties"]["run_id"]},
                },
            }
        ),
        name="work_inbox_start",
        description="Verify the retained native start for one unresolved pipeline receipt.",
        revision="work-inbox-start-v1",
        scope=scope,
        cursor_key=cursor_key,
        max_page_size=1,
        authorize=authorize_exact,
        load_authorized=load_accepted,
    )
    return WorkInbox(
        exact=exact,
        accepted=accepted,
        name="work_inbox",
        description="Find existing work, pending reviews and uncertain operations by current access. Follow next_cursor even after an empty page.",
        revision=revision,
        scope=scope,
        cursor_key=cursor_key,
        max_page_size=20,
        allow_empty_pages=True,
        authorize=authorized,
        load_authorized=load,
    )
