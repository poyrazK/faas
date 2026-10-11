"""Transaction-backed Customer Operation facts and workflow state."""

from __future__ import annotations

import datetime as dt
import hashlib
import json
import re
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from decimal import Decimal
from importlib.resources import files
from typing import Any
from uuid import uuid4

from ._operation_contract import (
    OPERATION_WORKFLOW_BLOCKER_ACTOR_BYTES,
    OPERATION_WORKFLOW_BLOCKER_ACTION_BYTES,
    OPERATION_WORKFLOW_BLOCKER_IMPACT_BYTES,
    OPERATION_IDENTITY_BYTES,
    OPERATION_MILESTONE_BATCH_BYTES,
    OPERATION_MILESTONE_PAYLOAD_BYTES,
    OPERATION_MILESTONES,
    OPERATION_REQUEST_BYTES,
    OPERATION_RESPONSE_BYTES,
    OPERATION_SUBJECT_ID_BYTES,
    OPERATION_WORKFLOW_STATE_BATCH_BYTES,
    OPERATION_WORKFLOW_STATE_REPORTS,
)
from .models.operation_milestone_request import OperationMilestoneRequest
from .models.operation_workflow_evidence_milestone import OperationWorkflowEvidenceMilestone
from .models.operation_workflow_state_report import OperationWorkflowStateReport
from .models.operation_workflow_dependency import OperationWorkflowDependency
from .models.operation_workflow_blocker import OperationWorkflowBlocker
from .models.operation_workflow_blocker_resolution import OperationWorkflowBlockerResolution
from .operations import OperationCommitUnknownError, OperationConflictError, OperationTransactionResult
from .models.operation_workflow_readiness_request import OperationWorkflowReadinessRequest
from .models.operation_workflow_readiness_response import OperationWorkflowReadinessResponse
from attrs import evolve
from .workflow_reconciliation import OperationWorkflowReconciliationInput, OperationWorkflowReconciliation, evaluate_reconciliation
from .models.operation_subject import OperationSubject
from .models.operation_milestones_response import OperationMilestonesResponse
from .business_compensation import OperationBusinessCompensation
from .business_effects import OperationBusinessEffect
from .models.operation_workflow_planned_effect import OperationWorkflowPlannedEffect
from .business_invariants import OperationBusinessInvariant
from .models.operation_workflow_planned_invariant import OperationWorkflowPlannedInvariant
from .business_decisions import OperationBusinessDecision
from .models.operation_workflow_planned_decision import OperationWorkflowPlannedDecision
from .types import UNSET

customer_operation_receipt_schema = files(__package__).joinpath("customer_operation_schema.sql").read_text(
    encoding="utf-8"
)

_SUPPORTED = object()
_UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\Z")
_METHOD = re.compile(r"[A-Z]+\Z")
_POSITIVE = re.compile(r"[1-9][0-9]*\Z")
_CAPABILITY = re.compile(r"[0-9a-f]{64}\Z")
_WORKFLOW = re.compile(r"[a-z][a-z0-9-]{0,62}\Z")
_STATE = re.compile(r"[a-z][a-z0-9-]{0,63}\Z")
_MILESTONE = re.compile(r"[a-z][a-z0-9-]{0,63}\Z")


class CustomerOperationConflictError(OperationConflictError):
    """The receipt has another owner or HTTP input."""


class CustomerOperationPublicationError(RuntimeError):
    """The business transaction committed but a public fact is still pending."""

    def __init__(self, operation_id: str, kind: str, cause: Exception) -> None:
        self.operation_id = operation_id
        self.kind = kind
        self.committed = True
        self.cause = cause
        super().__init__(
            f"business transaction committed; Customer Operation {kind} publication incomplete; "
            "retry the same Operation identity"
        )


@dataclass(frozen=True)
class CustomerOperationRequest:
    """Trusted Customer Operation context and exact original HTTP input."""

    operation_id: str
    account_id: str
    app_id: str
    platform_tenant_id: str
    result_max_bytes: int
    milestones_supported: bool
    method: str
    path: str
    body: bytes
    _support: object = field(default=None, repr=False, compare=False)


def _customer_uuid(value: str) -> str:
    if (
        not isinstance(value, str)
        or not _UUID.fullmatch(value.lower())
        or value.lower() == "00000000-0000-0000-0000-000000000000"
    ):
        raise ValueError("Customer Operation owner must be a nonzero UUID")
    return value.lower()


def _header_values(headers: Mapping[str, str | Sequence[str]], name: str) -> list[str]:
    values: list[str] = []
    for key, value in headers.items():
        if not isinstance(key, str):
            raise ValueError("Customer Operation header names must be strings")
        if key.lower() != name:
            continue
        entries = [value] if isinstance(value, str) else value
        if not isinstance(entries, Sequence) or any(not isinstance(item, str) for item in entries):
            raise ValueError("Customer Operation header values must be strings")
        values.extend(entries)
    return values


def _read_header(headers: Mapping[str, str | Sequence[str]], name: str, optional: bool = False) -> str:
    values = _header_values(headers, name)
    if optional and not values:
        return ""
    if len(values) != 1 or not values[0]:
        raise ValueError(f"Customer Operation requires one {name} header")
    return values[0]


def _normalize_request(request: CustomerOperationRequest) -> CustomerOperationRequest:
    if request._support is not _SUPPORTED:
        raise ValueError("use customer_operation_request_from_headers with negotiated support")
    if (
        type(request.result_max_bytes) is not int
        or not 0 < request.result_max_bytes <= OPERATION_RESPONSE_BYTES
        or type(request.milestones_supported) is not bool
        or not isinstance(request.method, str)
        or not _METHOD.fullmatch(request.method)
        or len(request.method) > OPERATION_IDENTITY_BYTES
        or not isinstance(request.path, str)
        or not request.path.startswith("/")
        or any(char in request.path for char in "\r\n\0")
        or not isinstance(request.body, bytes)
        or len(request.method.encode()) + len(request.path.encode("utf-8")) + len(request.body) > OPERATION_REQUEST_BYTES
    ):
        raise ValueError("invalid or oversized Customer Operation request")
    return CustomerOperationRequest(
        operation_id=_customer_uuid(request.operation_id),
        account_id=_customer_uuid(request.account_id),
        app_id=_customer_uuid(request.app_id),
        platform_tenant_id=_customer_uuid(request.platform_tenant_id),
        result_max_bytes=request.result_max_bytes,
        milestones_supported=request.milestones_supported,
        method=request.method,
        path=request.path,
        body=bytes(request.body),
        _support=_SUPPORTED,
    )


def customer_operation_request_from_headers(
    headers: Mapping[str, str | Sequence[str]], method: str, path: str, body: bytes
) -> CustomerOperationRequest:
    """Build context from headers delivered by Gregale's trusted guest listener.

    The reserved headers carry execution context; they do not authenticate an
    arbitrary HTTP request.
    """
    allowed_operation = {"x-gregale-operation-attempt", "x-gregale-operation-capability"}
    allowed_customer = {
        "x-gregale-customer-operation-id",
        "x-gregale-customer-operation-transaction-version",
        "x-gregale-customer-operation-result-max-bytes",
        "x-gregale-customer-operation-milestone-version",
    }
    for key in headers:
        if not isinstance(key, str):
            raise ValueError("Customer Operation header names must be strings")
        name = key.lower()
        if name.startswith("x-gregale-operation-") and name not in allowed_operation:
            raise ValueError("unsupported reserved Operation header")
        if name.startswith("x-gregale-customer-operation-") and name not in allowed_customer:
            raise ValueError("unsupported reserved Customer Operation header")

    if _read_header(headers, "x-gregale-customer-operation-transaction-version") != "1":
        raise ValueError("Customer Operation transactions are not supported")
    milestone_version = _read_header(headers, "x-gregale-customer-operation-milestone-version", optional=True)
    if milestone_version not in {"", "1"}:
        raise ValueError("unsupported Customer Operation milestone version")
    result_maximum = _read_header(headers, "x-gregale-customer-operation-result-max-bytes")
    if not _POSITIVE.fullmatch(result_maximum):
        raise ValueError("Customer Operation result byte limit is invalid")
    maximum = int(result_maximum)
    if maximum > OPERATION_RESPONSE_BYTES:
        raise ValueError("Customer Operation result byte limit exceeds the platform maximum")

    invocation_id = _read_header(headers, "x-faas-invocation-id")
    attempt = _read_header(headers, "x-gregale-operation-attempt")
    capability = _read_header(headers, "x-gregale-operation-capability")
    if (
        not _UUID.fullmatch(invocation_id)
        or invocation_id == "00000000-0000-0000-0000-000000000000"
        or not _POSITIVE.fullmatch(attempt)
        or int(attempt) > 2_147_483_647
        or not _CAPABILITY.fullmatch(capability)
    ):
        raise ValueError("Invalid Customer Operation execution context")

    return _normalize_request(
        CustomerOperationRequest(
            operation_id=_read_header(headers, "x-gregale-customer-operation-id"),
            account_id=_read_header(headers, "x-faas-tenant-id"),
            app_id=_read_header(headers, "x-faas-app-id"),
            platform_tenant_id=_read_header(headers, "x-faas-platform-tenant-id"),
            result_max_bytes=maximum,
            milestones_supported=milestone_version == "1",
            method=method,
            path=path,
            body=body,
            _support=_SUPPORTED,
        )
    )


def customer_operation_request_digest(input: CustomerOperationRequest) -> bytes:
    request = _normalize_request(input)
    prefix = f"gregale-customer-operation-request-v1\n{request.method}\n{request.path}\n".encode("utf-8")
    return hashlib.sha256(prefix + request.body).digest()


def _json_bytes(value: Any) -> bytes:
    return json.dumps(value, allow_nan=False, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def _reject_constant(value: str) -> None:
    raise ValueError(f"invalid JSON constant: {value}")


def _json_loads(value: str | bytes) -> Any:
    return json.loads(
        value,
        parse_constant=_reject_constant,
        parse_int=lambda raw: int(raw) if len(raw) < 20 else Decimal(raw),
        parse_float=Decimal,
    )


def _validate_result(body: bytes, maximum: int) -> None:
    if not body or len(body) > maximum:
        raise ValueError("Customer Operation result is empty or exceeds its byte limit")
    try:
        _json_loads(body.decode("utf-8"))
    except (UnicodeDecodeError, ValueError, json.JSONDecodeError) as error:
        raise ValueError("Customer Operation result is not valid JSON") from error


def _ready_connection(connection: Any) -> None:
    if connection.autocommit is not True or int(connection.info.transaction_status) != 0:
        raise ValueError("Customer Operation transaction requires an idle autocommit psycopg connection")


def _tuple_row(_cursor: Any):
    return tuple


class CustomerOperationReadinessError(ValueError):
    """Carries unmet requirements; the transaction cannot commit."""

    def __init__(self, response: OperationWorkflowReadinessResponse) -> None:
        super().__init__("Workflow transition readiness requirements are unmet")
        self.response = response


class CustomerOperationTransaction:
    """Cursor wrapper that queues public facts with the caller's database writes."""

    def __init__(self, cursor: Any, request: CustomerOperationRequest) -> None:
        self._cursor = cursor
        self._request = request
        self._milestones: list[OperationMilestoneRequest] = []
        self._workflow_states: list[OperationWorkflowStateReport] = []
        self._open = True
        self._guard_error: BaseException | None = None
        self._pending_guards = 0

    def __getattr__(self, name: str) -> Any:
        return getattr(self._cursor, name)

    def _check_open(self, kind: str) -> None:
        if not self._open:
            raise ValueError(f"{kind} must be recorded inside the transaction callback")
        if not self._request.milestones_supported:
            raise ValueError("Customer Operation milestone and workflow support is not negotiated")

    def milestone(self, name: str, payload: Any) -> None:
        self._check_open("Milestones")
        if not isinstance(name, str) or not _MILESTONE.fullmatch(name):
            raise ValueError("milestone name must be a bounded lowercase slug")
        if len(self._milestones) >= OPERATION_MILESTONES:
            raise ValueError("too many Customer Operation milestones")
        try:
            payload_bytes = _json_bytes(payload)
            if len(payload_bytes) > OPERATION_MILESTONE_PAYLOAD_BYTES:
                raise ValueError("milestone payload exceeds its byte limit")
            snapshot = json.loads(payload_bytes, parse_constant=_reject_constant)
            report = OperationMilestoneRequest(
                id=uuid4(), name=name, payload=snapshot, occurred_at=dt.datetime.now(dt.timezone.utc)
            )
            batch = _json_bytes({"milestones": [item.to_dict() for item in (*self._milestones, report)]})
        except (TypeError, ValueError, UnicodeError) as error:
            raise ValueError(f"invalid Customer Operation milestone: {error}") from error
        if len(batch) > OPERATION_MILESTONE_BATCH_BYTES:
            raise ValueError("Customer Operation milestone batch exceeds its byte limit")
        self._milestones.append(report)

    def report_business_invariant(self, milestone: str, input: OperationBusinessInvariant,
                                  current: list[OperationWorkflowBlocker]) -> list[OperationWorkflowBlocker]:
        """Evaluate in application code; supply the complete locked blocker head."""
        try:
            self._check_open("Invariant reports")
            if self._guard_error is not None: raise self._guard_error
            invariant = input.canonical()
            blockers = invariant.apply(current)
            prior_facts, prior_states = self._milestones, self._workflow_states
            self._milestones, self._workflow_states = list(prior_facts), list(prior_states)
            try:
                self.milestone(milestone, invariant.to_payload())
                self.workflow_blockers(invariant.workflow, invariant.instance_id, invariant.state, blockers)
            except BaseException:
                self._milestones, self._workflow_states = prior_facts, prior_states
                raise
            return blockers
        except BaseException as error:
            if self._guard_error is None: self._guard_error = error
            raise

    def business_compensation(self, milestone: str, compensation: OperationBusinessCompensation) -> None:
        """Record a reversal observation; never execute the reversal here."""
        self.milestone(milestone,compensation.to_payload())

    def business_effect(self, milestone: str, effect: OperationBusinessEffect) -> None:
        """Record a bounded observation; this does not execute an external effect."""
        self.milestone(milestone,effect.to_payload())

    def business_decision(self, milestone: str, decision: OperationBusinessDecision) -> None:
        """Queue reasoning as a declared milestone in the business transaction."""
        self.milestone(milestone, decision.to_payload())

    async def reconcile_workflow_state(
        self, milestone: str, scope: str, subject: OperationSubject,
        input: OperationWorkflowReconciliationInput,
        read: Callable[[dict[str, Any]], Awaitable[OperationMilestonesResponse]],
    ) -> OperationWorkflowReconciliation:
        """Compare a locked business row, recording discrepancies and safe snapshots."""
        self._pending_guards += 1
        try:
            self._check_open("Reconciliation")
            if self._guard_error is not None: raise self._guard_error
            input.validate()
            if not scope or not subject.type_ or not subject.id:
                raise ValueError("Reconciliation requires a scoped business reference")
            prior_counts = (len(self._milestones), len(self._workflow_states))
            page = await read({"app_id": self._request.app_id, "scope": scope,
                "subject_type": subject.type_, "subject_id": subject.id,
                "workflow": input.workflow, "workflow_instance_id": input.instance_id, "limit": 1})
            self._check_open("Reconciliation")
            if prior_counts != (len(self._milestones), len(self._workflow_states)):
                raise ValueError("Await reconciliation sequentially inside the callback")
            result = evaluate_reconciliation(input, page.workflow_instance)
            if result.status == "in_sync": return result
            prior_facts, prior_states = self._milestones, self._workflow_states
            self._milestones, self._workflow_states = list(prior_facts), list(prior_states)
            try:
                if result.refresh_needed:
                    self.workflow_state(input.workflow, input.instance_id, input.authoritative_state)
                self.milestone(milestone, result.to_payload())
                if result.refresh_needed:
                    fact = self._milestones[-1]
                    self._workflow_states[-1].evidence_milestones = [OperationWorkflowEvidenceMilestone(id=fact.id, name=fact.name)]
            except BaseException:
                self._milestones, self._workflow_states = prior_facts, prior_states
                raise
            return result
        except BaseException as error:
            if self._guard_error is None: self._guard_error = error
            raise
        finally:
            self._pending_guards -= 1

    def workflow_state(self, workflow: str, instance_id: str, state: str) -> None:
        self._queue_workflow_state(workflow, instance_id, state, None)

    def workflow_transition(self, workflow: str, instance_id: str, from_state: str, to_state: str) -> None:
        self._queue_workflow_state(workflow, instance_id, to_state, from_state)

    async def guarded_workflow_transition(
        self, proposal: OperationWorkflowReadinessRequest,
        facts: Sequence[tuple[str, Any]],
        check: Callable[[OperationWorkflowReadinessRequest], Awaitable[OperationWorkflowReadinessResponse]],
    ) -> OperationWorkflowReadinessResponse:
        """Check after locking business rows, before writing; await inside callback."""
        self._pending_guards += 1
        try:
            self._check_open("Readiness guards")
            if self._guard_error is not None:
                raise self._guard_error
            if (str(proposal.app_id) != self._request.app_id or proposal.tenant_id is not UNSET
                or type(proposal.state_revision) is not int or proposal.state_revision <= 0
                or type(proposal.contract_version) is not int or proposal.contract_version <= 0):
                raise ValueError("Guard requires matching app and positive locked revision and contract version")
            decisions = [OperationWorkflowPlannedDecision(name, OperationBusinessDecision(**payload["decision"])) for name, payload in facts if isinstance(payload, dict) and payload.get("kind") == "gregale.business-decision.v1"]
            for planned in decisions: planned.decision.to_payload()
            invariants=[OperationWorkflowPlannedInvariant(name,OperationBusinessInvariant(**payload["invariant"]).canonical()) for name,payload in facts if isinstance(payload,dict) and payload.get("kind")=="gregale.business-invariant.v1"]
            effects=[OperationWorkflowPlannedEffect(name,OperationBusinessEffect(**payload["effect"])) for name,payload in facts if isinstance(payload,dict) and payload.get("kind")=="gregale.business-effect.v1"]
            for planned in effects: planned.effect.to_payload()
            request = evolve(proposal, subject=evolve(proposal.subject), effects=effects, invariants=invariants, decisions=decisions, milestones=list(dict.fromkeys(name for name, _ in facts)))
            prior_facts, prior_states = self._milestones, self._workflow_states
            prior_fact_count, prior_state_count = len(prior_facts), len(prior_states)
            self._milestones, self._workflow_states = list(prior_facts), list(prior_states)
            try:
                for name, payload in facts:
                    self.milestone(name, payload)
                self.workflow_transition(request.workflow, request.instance_id, request.from_state, request.to_state)
                staged_facts, staged_states = self._milestones, self._workflow_states
            finally:
                self._milestones, self._workflow_states = prior_facts, prior_states
            response = await check(request)
            self._check_open("Readiness guards")
            if len(self._milestones) != prior_fact_count or len(self._workflow_states) != prior_state_count:
                raise ValueError("Do not queue evidence concurrently with a readiness guard")
            r = response.readiness
            for pending in reversed(self._workflow_states):
                if pending.workflow != request.workflow or pending.instance_id != request.instance_id or pending.blockers_only is not True:
                    continue
                for blocker in pending.blockers:
                    if blocker.operation != request.operation or not blocker.code.startswith("invariant-"): continue
                    if not any(b.code == blocker.code and b.operation == blocker.operation for b in r.blockers):
                        r.blockers.append(blocker)
                    if r.invariant_blockers is UNSET: r.invariant_blockers = []
                    if not any(b.code == blocker.code and b.operation == blocker.operation for b in r.invariant_blockers):
                        r.invariant_blockers.append(blocker)
                    if "application_blocked" not in r.reasons: r.reasons.append("application_blocked")
                    r.ready = False
                break
            if (response.subject != request.subject or response.workflow != request.workflow
                or response.instance_id != request.instance_id or r.transition.operation != request.operation
                or r.transition.from_ != request.from_state or r.transition.to != request.to_state):
                raise ValueError("Readiness response does not match proposed transition")
            if (not r.ready or not r.declared or r.state_revision != request.state_revision
                or r.contract_version != request.contract_version or r.reasons or r.blockers
                or r.unmet_dependencies or r.missing_milestones or (r.missing_policies is not UNSET and r.missing_policies) or (r.missing_dependency_workflows is not UNSET and r.missing_dependency_workflows) or (r.unmet_invariants is not UNSET and r.unmet_invariants) or (r.unmet_effects is not UNSET and r.unmet_effects)):
                raise CustomerOperationReadinessError(response)
            self._milestones, self._workflow_states = staged_facts, staged_states
            return response
        except BaseException as error:
            if self._guard_error is None:
                self._guard_error = error
            raise
        finally:
            self._pending_guards -= 1

    def workflow_dependencies(self, workflow: str, instance_id: str, state: str, dependencies: list[OperationWorkflowDependency]) -> None:
        """Replace direct prerequisites; [] removes all links while preserving blockers."""
        canonical=_canonical_workflow_dependencies(dependencies)
        self._queue_workflow_state(workflow, instance_id, state, state)
        report=self._workflow_states[-1]
        report.depends_on,report.dependencies_only=canonical,True
        if len(_json_bytes({"workflow_states":[item.to_dict() for item in self._workflow_states]})) > OPERATION_WORKFLOW_STATE_BATCH_BYTES:
            self._workflow_states.pop()
            raise ValueError("dependency report batch exceeds its byte limit")

    def workflow_outcome(self, workflow: str, instance_id: str, state: str, code: str, description: str) -> None:
        """Report an explicit terminal outcome, preserving current blockers and deadline."""
        _validate_workflow_outcome(code, description)
        self._queue_workflow_state(workflow, instance_id, state, state)
        report = self._workflow_states[-1]
        report.outcome_code, report.outcome_description, report.outcome_only = code, description, True
        if len(_json_bytes({"workflow_states": [item.to_dict() for item in self._workflow_states]})) > OPERATION_WORKFLOW_STATE_BATCH_BYTES:
            self._workflow_states.pop()
            raise ValueError("workflow outcome batch exceeds its byte limit")

    def workflow_deadline(self, workflow: str, instance_id: str, state: str, due_at: str) -> None:
        """Set/update the business deadline; an empty string clears. Preserve current blockers."""
        due_at = _canonical_workflow_deadline(due_at)
        self._queue_workflow_state(workflow, instance_id, state, state)
        report = self._workflow_states[-1]
        report.deadline_at = due_at
        report.deadline_only = True
        if len(_json_bytes({"workflow_states": [item.to_dict() for item in self._workflow_states]})) > OPERATION_WORKFLOW_STATE_BATCH_BYTES:
            self._workflow_states.pop()
            raise ValueError("workflow deadline batch exceeds its byte limit")

    def workflow_blockers(self, workflow: str, instance_id: str, state: str, blockers: list[OperationWorkflowBlocker], *, resolutions: list[OperationWorkflowBlockerResolution] | None = None) -> None:
        """Replace public blockers while preserving the locked business row state; [] clears."""
        canonical = _canonical_workflow_blockers(blockers)
        canonical_resolutions = _canonical_workflow_resolutions([] if resolutions is None else resolutions, canonical)
        self._queue_workflow_state(workflow, instance_id, state, state)
        report = self._workflow_states[-1]
        report.blockers = canonical
        report.blockers_only = True
        report.blocker_resolutions = canonical_resolutions
        if len(_json_bytes({"workflow_states": [item.to_dict() for item in self._workflow_states]})) > OPERATION_WORKFLOW_STATE_BATCH_BYTES:
            self._workflow_states.pop()
            raise ValueError("workflow blocker batch exceeds its byte limit")

    def _queue_workflow_state(self, workflow: str, instance_id: str, state: str, from_state: str | None) -> None:
        self._check_open("Workflow state")
        try:
            instance_bytes = instance_id.encode("utf-8") if isinstance(instance_id, str) else b""
        except UnicodeEncodeError as error:
            raise ValueError("workflow instance ID must be valid UTF-8") from error
        if (
            not isinstance(workflow, str)
            or not _WORKFLOW.fullmatch(workflow)
            or not isinstance(state, str)
            or not _STATE.fullmatch(state)
            or (from_state is not None and (not isinstance(from_state, str) or not _STATE.fullmatch(from_state)))
            or not isinstance(instance_id, str)
            or not instance_bytes
            or len(instance_bytes) > OPERATION_SUBJECT_ID_BYTES
            or any(ord(char) < 0x20 or ord(char) == 0x7F for char in instance_id)
        ):
            raise ValueError("workflow state requires valid workflow, instance ID, and state names")
        if len(self._workflow_states) >= OPERATION_WORKFLOW_STATE_REPORTS:
            raise ValueError("too many Customer Operation workflow state reports")

        kwargs: dict[str, Any] = {
            "id": uuid4(),
            "workflow": workflow,
            "instance_id": instance_id,
            "state": state,
            "revision": 0,
            "occurred_at": dt.datetime.now(dt.timezone.utc),
        }
        if from_state is not None:
            kwargs["from_state"] = from_state
        report = OperationWorkflowStateReport(**kwargs)
        try:
            batch = _json_bytes({"workflow_states": [item.to_dict() for item in (*self._workflow_states, report)]})
        except (TypeError, ValueError, UnicodeError) as error:
            raise ValueError(f"invalid Customer Operation workflow state: {error}") from error
        if len(batch) > OPERATION_WORKFLOW_STATE_BATCH_BYTES:
            raise ValueError("Customer Operation workflow state batch exceeds its byte limit")
        self._workflow_states.append(report)


async def awith_customer_operation_transaction(
    connection: Any,
    input: CustomerOperationRequest,
    handler: Callable[[CustomerOperationTransaction], Awaitable[Any]],
    validate_milestones: Callable[[list[OperationMilestoneRequest]], Awaitable[None]] | None = None,
    validate_workflow_states: Callable[
        [list[OperationWorkflowStateReport], list[OperationMilestoneRequest]], Awaitable[None]
    ]
    | None = None,
) -> OperationTransactionResult:
    """Commit business writes, the response receipt, and customer outbox rows atomically.

    The connection must be an idle, exclusively leased psycopg AsyncConnection
    with ``autocommit=True``. The callback must not commit or perform external
    side effects. Receipt replay skips the callback.
    """
    request = _normalize_request(input)
    digest = customer_operation_request_digest(request)
    _ready_connection(connection)
    completed = False
    try:
        async with connection.transaction():
            async with connection.cursor(row_factory=_tuple_row) as cursor:
                await cursor.execute("SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
                await cursor.execute(
                    "SELECT pg_advisory_xact_lock(hashtextextended(%s || %s::uuid::text, 0))",
                    ("gregale.customer-operation-inbox.v1:", request.operation_id),
                )
                await cursor.execute(
                    "SELECT account_id::text,app_id::text,coalesce(platform_tenant_id::text,''),request_digest,response_body "
                    "FROM public.gregale_customer_operation_inbox WHERE operation_id=%s::uuid",
                    (request.operation_id,),
                )
                stored = await cursor.fetchone()
                replayed = stored is not None
                if stored is not None:
                    if (
                        stored[0] != request.account_id
                        or stored[1] != request.app_id
                        or stored[2] != request.platform_tenant_id
                        or bytes(stored[3]) != digest
                    ):
                        raise CustomerOperationConflictError("customer Operation receipt scope or input differs")
                    body = stored[4].encode("utf-8") if isinstance(stored[4], str) else bytes(stored[4])
                    _validate_result(body, request.result_max_bytes)
                else:
                    if request.milestones_supported:
                        for table in (
                            "gregale_customer_operation_milestones",
                            "gregale_customer_operation_workflow_state_counters",
                            "gregale_customer_operation_workflow_states",
                        ):
                            await cursor.execute(f"SELECT 1 FROM public.{table} LIMIT 0")

                    async with connection.cursor() as business_cursor:
                        transaction = CustomerOperationTransaction(business_cursor, request)
                        try:
                            result = await handler(transaction)
                        finally:
                            transaction._open = False
                    if transaction._pending_guards:
                        raise ValueError("Readiness guards must be awaited before the callback returns")
                    if transaction._guard_error is not None:
                        raise transaction._guard_error
                    try:
                        body = _json_bytes(result)
                    except (TypeError, ValueError, UnicodeError) as error:
                        raise ValueError(f"Customer Operation result must contain JSON values: {error}") from error
                    _validate_result(body, request.result_max_bytes)

                    if transaction._milestones:
                        if validate_milestones is None:
                            raise ValueError("milestone validation is required before commit")
                        await validate_milestones(transaction._milestones)
                        for report in transaction._milestones:
                            await cursor.execute(
                                "INSERT INTO public.gregale_customer_operation_milestones "
                                "(operation_id,id,name,payload,occurred_at) VALUES (%s::uuid,%s::uuid,%s,%s,%s::timestamptz)",
                                (
                                    request.operation_id,
                                    str(report.id),
                                    report.name,
                                    _json_bytes(report.payload).decode("utf-8"),
                                    report.occurred_at,
                                ),
                            )

                    if transaction._workflow_states:
                        saved = await _save_workflow_states(
                            cursor, request, transaction._workflow_states, transaction._milestones
                        )
                        validation_batch = _json_bytes(
                            {
                                "workflow_states": [report.to_dict() for report in saved],
                                "milestones": [report.to_dict() for report in transaction._milestones],
                            }
                        )
                        if len(validation_batch) > OPERATION_WORKFLOW_STATE_BATCH_BYTES:
                            raise ValueError("Customer Operation workflow validation batch exceeds its byte limit")
                        if validate_workflow_states is None:
                            raise ValueError("workflow state validation is required before commit")
                        await validate_workflow_states(saved, transaction._milestones)

                    await cursor.execute(
                        "INSERT INTO public.gregale_customer_operation_inbox "
                        "(operation_id,account_id,app_id,platform_tenant_id,request_digest,response_body) "
                        "VALUES (%s::uuid,%s::uuid,%s::uuid,%s::uuid,%s,%s)",
                        (
                            request.operation_id,
                            request.account_id,
                            request.app_id,
                            request.platform_tenant_id,
                            digest,
                            body.decode("utf-8"),
                        ),
                    )
            completed = True
    except Exception as error:
        if completed:
            raise OperationCommitUnknownError(
                "Customer Operation commit outcome unknown; retry the same Operation identity"
            ) from error
        raise
    return OperationTransactionResult(body=body, replayed=replayed)


async def _save_workflow_states(
    cursor: Any,
    request: CustomerOperationRequest,
    reports: list[OperationWorkflowStateReport],
    milestones: list[OperationMilestoneRequest],
) -> list[OperationWorkflowStateReport]:
    evidence_by_name: dict[str, OperationWorkflowEvidenceMilestone] = {}
    for milestone in milestones:
        evidence_by_name.setdefault(
            milestone.name,
            OperationWorkflowEvidenceMilestone(id=milestone.id, name=milestone.name),
        )
    evidence = sorted(evidence_by_name.values(), key=lambda item: (item.name, str(item.id)))
    if len(evidence) > 16:
        raise ValueError("workflow transition evidence exceeds its limit")

    saved: list[OperationWorkflowStateReport] = []
    for report in reports:
        await cursor.execute(
            "INSERT INTO public.gregale_customer_operation_workflow_state_counters "
            "(platform_tenant_id,workflow,instance_id,revision) VALUES (%s::uuid,%s,%s,1) "
            "ON CONFLICT (platform_tenant_id,workflow,instance_id) DO UPDATE "
            "SET revision=public.gregale_customer_operation_workflow_state_counters.revision+1 "
            "WHERE public.gregale_customer_operation_workflow_state_counters.revision<9007199254740991 "
            "RETURNING revision",
            (request.platform_tenant_id, report.workflow, report.instance_id),
        )
        counter = await cursor.fetchone()
        if counter is None or type(counter[0]) is not int or not 1 <= counter[0] <= 9_007_199_254_740_991:
            raise ValueError("workflow state revision is unavailable")
        revision = counter[0]

        await cursor.execute(
            "SELECT last_state,last_blockers,last_blockers_revision,last_deadline_at,last_deadline_revision,last_outcome_code,last_outcome_description,last_outcome_revision,last_dependencies,last_dependencies_revision FROM public.gregale_customer_operation_workflow_state_counters "
            "WHERE platform_tenant_id=%s::uuid AND workflow=%s AND instance_id=%s FOR UPDATE",
            (request.platform_tenant_id, report.workflow, report.instance_id),
        )
        head = await cursor.fetchone()
        if head is None:
            raise ValueError("workflow state counter is unavailable")
        last_state = head[0]
        from_state = report.from_state if report.from_state is not UNSET else ""
        if last_state is not None and (not isinstance(last_state, str) or not _STATE.fullmatch(last_state)):
            raise ValueError("saved workflow state is invalid")
        if from_state and last_state is not None and last_state != from_state:
            raise ValueError("workflow transition source does not match the latest app-reported state")

        state_evidence: list[OperationWorkflowEvidenceMilestone] = []
        if from_state and report.blockers_only is not True and report.deadline_only is not True and report.outcome_only is not True and report.dependencies_only is not True and evidence:
            state_evidence = evidence
        report_data = report.to_dict()
        report_data["revision"] = revision
        if state_evidence:
            report_data["evidence_milestones"] = [item.to_dict() for item in state_evidence]
        elif report.evidence_milestones is UNSET:
            report_data.pop("evidence_milestones", None)
        previous = head[1] if not isinstance(head[1], str) else json.loads(head[1])
        if report.deadline_only is True or report.outcome_only is True or report.dependencies_only is True:
            if head[2] != revision - 1:
                raise ValueError("metadata update requires current blocker metadata; upgrade all writers")
            report_data["blockers"] = previous
        if report.deadline_only is not True and report_data.get("deadline_at", "") == "" and head[4] == revision - 1:
            report_data["deadline_at"] = _canonical_workflow_deadline(head[3])
        if report.outcome_only is not True and not report_data.get("outcome_code") and last_state == report.state and head[7] == revision - 1:
            if head[5]: report_data["outcome_code"], report_data["outcome_description"] = head[5], head[6]
        if report_data.get("outcome_code") or report_data.get("outcome_description"):
            _validate_workflow_outcome(report_data.get("outcome_code"), report_data.get("outcome_description"))
        if report.dependencies_only is not True and "depends_on" not in report_data and head[9] == revision - 1:
            last_dependencies=head[8] if not isinstance(head[8],str) else json.loads(head[8])
            report_data["depends_on"]=last_dependencies
        dependencies=_canonical_workflow_dependencies([OperationWorkflowDependency.from_dict(v) for v in report_data.get("depends_on",[])])
        report_data["depends_on"]=[d.to_dict() for d in dependencies]
        if "deadline_at" in report_data: report_data["deadline_at"] = _canonical_workflow_deadline(report_data["deadline_at"])
        previous = {(b["operation"], b["code"]): b for b in previous}
        for b in report_data.get("blockers", []):
            if head[2] == revision - 1:
                prior = previous.get((b["operation"], b["code"]))
                if prior is not None and prior.get("first_observed_at"):
                    b["first_observed_at"] = prior["first_observed_at"]
                elif prior is None and "first_observed_at" not in b:
                    b["first_observed_at"] = report.occurred_at.astimezone(dt.timezone.utc).isoformat()
            if "first_observed_at" in b and dt.datetime.fromisoformat(b["first_observed_at"].replace("Z", "+00:00")) > report.occurred_at:
                raise ValueError("blocker first observation exceeds report time")
            if b.get("acknowledged_at"):
                ack = dt.datetime.fromisoformat(b["acknowledged_at"].replace("Z", "+00:00"))
                if ack > report.occurred_at or b.get("first_observed_at") and ack < dt.datetime.fromisoformat(b["first_observed_at"].replace("Z", "+00:00")):
                    raise ValueError("blocker acknowledgement is outside observation/report interval")
        saved_report = OperationWorkflowStateReport.from_dict(report_data)
        evidence_json = _json_bytes([item.to_dict() for item in state_evidence]).decode("utf-8")

        await cursor.execute(
            "INSERT INTO public.gregale_customer_operation_workflow_states "
            "(operation_id,id,platform_tenant_id,workflow,instance_id,from_state,state,revision,evidence_milestones,occurred_at,blockers,blockers_only,blocker_resolutions,deadline_at,deadline_only,outcome_code,outcome_description,outcome_only,depends_on,dependencies_only) "
            "VALUES (%s::uuid,%s::uuid,%s::uuid,%s,%s,%s,%s,%s,%s::jsonb,%s::timestamptz,%s::jsonb,%s,%s::jsonb,%s,%s,%s,%s,%s,%s::jsonb,%s)",
            (
                request.operation_id,
                str(saved_report.id),
                request.platform_tenant_id,
                saved_report.workflow,
                saved_report.instance_id,
                from_state,
                saved_report.state,
                saved_report.revision,
                evidence_json,
                saved_report.occurred_at,
                _json_bytes([b.to_dict() for b in saved_report.blockers] if saved_report.blockers is not UNSET else []).decode("utf-8"),
                saved_report.blockers_only is True,
                _json_bytes([v.to_dict() for v in saved_report.blocker_resolutions] if saved_report.blocker_resolutions is not UNSET else []).decode("utf-8"),
                saved_report.deadline_at if saved_report.deadline_at is not UNSET else "",
                saved_report.deadline_only is True,
                report_data.get("outcome_code", ""),
                report_data.get("outcome_description", ""),
                saved_report.outcome_only is True,
                _json_bytes(report_data["depends_on"]).decode("utf-8"),
                saved_report.dependencies_only is True,
            ),
        )
        await cursor.execute(
            "UPDATE public.gregale_customer_operation_workflow_state_counters SET last_state=%s,last_blockers=%s::jsonb,last_blockers_revision=%s,last_deadline_at=%s,last_deadline_revision=%s,last_outcome_code=%s,last_outcome_description=%s,last_outcome_revision=%s,last_dependencies=%s::jsonb,last_dependencies_revision=%s "
            "WHERE platform_tenant_id=%s::uuid AND workflow=%s AND instance_id=%s AND revision=%s RETURNING revision",
            (
                saved_report.state,
                _json_bytes(report_data.get("blockers", [])).decode("utf-8"),
                revision,
                report_data.get("deadline_at", ""),
                revision,
                report_data.get("outcome_code", ""),
                report_data.get("outcome_description", ""),
                revision,
                _json_bytes(report_data["depends_on"]).decode("utf-8"),
                revision,
                request.platform_tenant_id,
                saved_report.workflow,
                saved_report.instance_id,
                saved_report.revision,
            ),
        )
        updated = await cursor.fetchone()
        if updated is None or updated[0] != saved_report.revision:
            raise ValueError("workflow state counter changed unexpectedly")
        saved.append(saved_report)
    return saved

def _canonical_blocker_text(value, max_bytes: int):
    if value is UNSET or value == "":
        return UNSET
    if not isinstance(value, str) or any(ord(c) < 0x20 or ord(c) == 0x7f or 0xd800 <= ord(c) <= 0xdfff for c in value):
        raise ValueError("invalid public blocker assignment or attribution")
    if len(value.encode("utf-8")) > max_bytes:
        raise ValueError("public blocker assignment or attribution exceeds byte limit")
    return value

def _canonical_workflow_blockers(blockers: list[OperationWorkflowBlocker]) -> list[OperationWorkflowBlocker]:
    if not isinstance(blockers, list) or len(blockers) > 16:
        raise ValueError("workflow blockers require at most 16 public reasons")
    result: list[OperationWorkflowBlocker] = []
    seen: set[tuple[str, str]] = set()
    for b in blockers:
        if (not isinstance(b, OperationWorkflowBlocker) or not isinstance(b.code, str) or not _STATE.fullmatch(b.code)
            or not isinstance(b.operation, str) or not _STATE.fullmatch(b.operation)
            or not isinstance(b.description, str) or not b.description or len(b.description.encode("utf-8")) > 512
            or any(ord(c) < 0x20 or ord(c) == 0x7f for c in b.description) or (b.operation, b.code) in seen):
            raise ValueError("invalid workflow blocker public fields or duplicate target/code")
        seen.add((b.operation, b.code))
        priority = b.priority
        if priority is not UNSET and priority not in ("", "low", "normal", "high", "urgent"):
            raise ValueError("invalid blocker priority")
        if priority == "": priority = UNSET
        first = b.first_observed_at
        if first is not UNSET:
            if isinstance(first, str):
                if not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})", first):
                    raise ValueError("invalid blocker observation time")
                first = dt.datetime.fromisoformat(first.replace("Z", "+00:00"))
            if not isinstance(first, dt.datetime):
                raise ValueError("invalid blocker observation time")
            if first.tzinfo is None or first.utcoffset() is None:
                raise ValueError("blocker observation needs a timezone")
            first = first.astimezone(dt.timezone.utc)
        def canonical_time(value):
            if value is UNSET or value == "": return UNSET
            if isinstance(value, str):
                if not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})", value):
                    raise ValueError("invalid blocker acknowledgement timestamp")
                value = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
            if not isinstance(value, dt.datetime) or value.tzinfo is None or value.utcoffset() is None:
                raise ValueError("blocker timestamp needs a timezone")
            return value.astimezone(dt.timezone.utc)
        ack, due = canonical_time(b.acknowledged_at), canonical_time(b.follow_up_at)
        actor = _canonical_blocker_text(b.acknowledged_by, OPERATION_WORKFLOW_BLOCKER_ACTOR_BYTES)
        if (ack is UNSET) != (actor is UNSET) or due is not UNSET and ack is UNSET or ack is not UNSET and first is not UNSET and ack < first or due is not UNSET and due < ack:
            raise ValueError("invalid blocker acknowledgement or follow-up")
        result.append(OperationWorkflowBlocker(priority=priority, business_impact=_canonical_blocker_text(b.business_impact, OPERATION_WORKFLOW_BLOCKER_IMPACT_BYTES), acknowledged_at=ack, acknowledged_by=actor, follow_up_at=due, code=b.code, description=b.description, operation=b.operation, first_observed_at=first,
            owner=_canonical_blocker_text(b.owner, OPERATION_WORKFLOW_BLOCKER_ACTOR_BYTES), next_action=_canonical_blocker_text(b.next_action, OPERATION_WORKFLOW_BLOCKER_ACTION_BYTES)))
    return sorted(result, key=lambda b: (b.operation, b.code))

def _canonical_workflow_resolutions(resolutions: list[OperationWorkflowBlockerResolution], blockers: list[OperationWorkflowBlocker]) -> list[OperationWorkflowBlockerResolution]:
    if not isinstance(resolutions, list) or len(resolutions) > 16:
        raise ValueError("workflow resolutions require at most 16 public facts")
    active = {(b.operation, b.code) for b in blockers}
    fields: list[OperationWorkflowBlocker] = []
    result: list[OperationWorkflowBlockerResolution] = []
    for v in resolutions:
        if (not isinstance(v, OperationWorkflowBlockerResolution)
            or not _UUID.fullmatch(str(v.blocker_operation_id)) or str(v.blocker_operation_id) == "00000000-0000-0000-0000-000000000000"
            or not _UUID.fullmatch(str(v.blocker_report_id)) or str(v.blocker_report_id) == "00000000-0000-0000-0000-000000000000"
            or type(v.blocker_revision) is not int or not 1 <= v.blocker_revision <= 9_007_199_254_740_991):
            raise ValueError("resolution requires a prior report identity and revision")
        fields.append(OperationWorkflowBlocker(code=v.code, operation=v.operation, description=v.description))
        resolved = OperationWorkflowBlockerResolution.from_dict(v.to_dict())
        mid, name, oid = v.verification_milestone_id, v.verification_milestone_name, v.verification_operation_id
        mid = UNSET if mid == "" else mid
        name = UNSET if name == "" else name
        oid = UNSET if oid == "" else oid
        owner = _canonical_blocker_text(v.verification_owner, OPERATION_WORKFLOW_BLOCKER_ACTOR_BYTES)
        if (mid is UNSET) != (name is UNSET) or mid is UNSET and (oid is not UNSET or owner is not UNSET):
            raise ValueError("invalid resolution verification requirement")
        if mid is not UNSET and (not isinstance(mid, str) or not _UUID.fullmatch(mid) or mid == "00000000-0000-0000-0000-000000000000" or not isinstance(name, str) or not _STATE.fullmatch(name)):
            raise ValueError("invalid resolution verification milestone")
        if oid is not UNSET and (not isinstance(oid, str) or not _UUID.fullmatch(oid) or oid == "00000000-0000-0000-0000-000000000000"):
            raise ValueError("invalid resolution verification Operation")
        resolved.verification_milestone_id, resolved.verification_milestone_name = mid, name
        resolved.verification_operation_id, resolved.verification_owner = oid, owner
        resolved.resolved_by = _canonical_blocker_text(v.resolved_by, OPERATION_WORKFLOW_BLOCKER_ACTOR_BYTES)
        result.append(resolved)
    canonical_fields = _canonical_workflow_blockers(fields)
    if any((v.operation, v.code) in active for v in canonical_fields):
        raise ValueError("a resolved blocker cannot remain in the replacement list")
    return sorted(result, key=lambda v: (v.operation, v.code))

def _canonical_workflow_deadline(value: str) -> str:
    if value == "": return ""
    if not isinstance(value, str) or not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})", value):
        raise ValueError("deadline requires a finite RFC3339 timestamp")
    due = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    return due.astimezone(dt.timezone.utc).isoformat()

def _validate_workflow_outcome(code: str, description: str) -> None:
    if not isinstance(code, str) or not _STATE.fullmatch(code) or not isinstance(description, str) or not description or len(description.encode("utf-8")) > 512 or any(ord(c) < 0x20 or ord(c) == 0x7f for c in description):
        raise ValueError("outcome requires a bounded public code and description")

def _canonical_workflow_dependencies(value: list[OperationWorkflowDependency]) -> list[OperationWorkflowDependency]:
    if not isinstance(value,list) or len(value)>16: raise ValueError("at most 16 direct dependencies are supported")
    result=[];seen=set()
    def valid_id(value: str) -> bool:
        return isinstance(value,str) and bool(value) and len(value.encode("utf-8"))<=256 and not any(ord(c)<0x20 or ord(c)==0x7f for c in value)
    for d in value:
        if not isinstance(d,OperationWorkflowDependency) or not isinstance(d.subject_type,str) or not _STATE.fullmatch(d.subject_type) or not valid_id(d.subject_id) or not isinstance(d.workflow,str) or not _WORKFLOW.fullmatch(d.workflow) or not valid_id(d.instance_id): raise ValueError("invalid dependency reference")
        key=(d.subject_type,d.subject_id,d.workflow,d.instance_id)
        if key in seen: raise ValueError("duplicate dependency")
        seen.add(key);code=d.required_outcome_code
        if code is not UNSET and (not isinstance(code,str) or code and not _STATE.fullmatch(code)): raise ValueError("invalid required outcome")
        result.append(OperationWorkflowDependency(subject_type=d.subject_type,subject_id=d.subject_id,workflow=d.workflow,instance_id=d.instance_id,required_outcome_code=code if code else UNSET))
    return sorted(result,key=lambda d:(d.subject_type,d.subject_id,d.workflow,d.instance_id))
