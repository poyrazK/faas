from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_request_mode import EventRecoveryRequestMode, check_event_recovery_request_mode
from ..models.event_recovery_request_outcome import EventRecoveryRequestOutcome, check_event_recovery_request_outcome
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_workflow_recovery_plan import EventWorkflowRecoveryPlan
    from ..models.workflow_run_recovery_plan import WorkflowRunRecoveryPlan


T = TypeVar("T", bound="EventRecoveryRequest")


@_attrs_define
class EventRecoveryRequest:
    """Select routing failures (default) or the latest replayable retained execution per application event consumer. With
    parent_job_id, saved parent failures remain selectable even when execution or receipt evidence has disappeared;
    admission skips changes rather than following newer work. Ordinary execution mode includes publication and
    materialized backfill recipients, excludes workflows and object notifications, and requires retained admission and
    execution records. Native workflow mode requires explicit workflow_runs and never selects event recipients. Creation
    freezes its own selection; preview is advisory.

    """

    workflow_runs: WorkflowRunRecoveryPlan | Unset = UNSET
    """Explicit reviewed native workflow runs. Requires workflow mode and request_id; excludes event selections and
    receipt protection. Each resume checks the original count and eligibility atomically with its job outcome.
    Concurrency pressure leaves the item pending; changed or unsafe targets are skipped. Queued means a resume was
    admitted, not execution success. No new runs are selected. The original normalized request returns the retained
    job on retry; changes conflict."""
    workflow_routing: EventWorkflowRecoveryPlan | Unset = UNSET
    """Explicit reviewed workflow acceptance recipients. Routing mode only, requires request_id and excludes parent
    and backlog filters. Creation freezes the exact selection. Before each admission the worker atomically compares
    published version, replay count and routing generation; changes are skipped. Repeated request_id returns the
    retained job, even after completion; a different normalized plan or rate conflicts. Preview and preflight are
    advisory; workflow fence eligibility is unknown in preflight. Queued means routing recovery was admitted, not
    workflow completion."""
    parent_job_id: UUID | Unset = UNSET
    """Select saved failed/dead-lettered execution items or saved failed native workflow items from a retained
    terminal recovery in the same account/app. Native workflow retries require explicit parent_position and the
    saved resume count for every target, and the same workflow name. Changed runs are rejected at creation or
    skipped at admission. Does not follow newer replays."""
    request_id: UUID | Unset = UNSET
    """Required for workflow_runs, workflow_routing and on child creation; optional on child preview. Account-
    scoped durable idempotency while the job is retained. Repeating the normalized selection returns its existing
    job; different selection/app with the same UUID conflicts. Only allowed with parent_job_id, workflow_runs or
    workflow_routing. Operator reason is excluded from comparison and the original audit reason wins."""
    reason: str | Unset = UNSET
    """Optional operator reason, limited to 512 UTF-8 bytes without control characters. Stored only in audit
    history; omitted from frozen selection. Preview does not record it."""
    mode: EventRecoveryRequestMode | Unset = "routing"
    outcome: EventRecoveryRequestOutcome | Unset = UNSET
    """Execution mode only; omitted selects both replayable outcomes."""
    subscription_id: str | Unset = UNSET
    event_source: str | Unset = UNSET
    event_type: str | Unset = UNSET
    failure_code: str | Unset = UNSET
    min_age_seconds: int | Unset = 0
    """Minimum age of the recorded terminal failure."""
    protect_receipts: bool | Unset = False
    """Hold selected retained receipts from pruning while their items are pending and the job is active and
    unexpired. Pausing does not extend the existing 24-hour lifetime. Preview acquires no holds. Held receipts still
    count toward account storage limits."""
    include_non_retryable: bool | Unset = False
    rate_per_second: int | Unset = 10
    """Maximum recipients processed per job in a one-second window; zero uses the default. Actual throughput
    depends on scheduler load."""

    def to_dict(self) -> dict[str, Any]:
        workflow_runs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.workflow_runs, Unset):
            workflow_runs = self.workflow_runs.to_dict()

        workflow_routing: dict[str, Any] | Unset = UNSET
        if not isinstance(self.workflow_routing, Unset):
            workflow_routing = self.workflow_routing.to_dict()

        parent_job_id: str | Unset = UNSET
        if not isinstance(self.parent_job_id, Unset):
            parent_job_id = str(self.parent_job_id)

        request_id: str | Unset = UNSET
        if not isinstance(self.request_id, Unset):
            request_id = str(self.request_id)

        reason = self.reason

        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        outcome: str | Unset = UNSET
        if not isinstance(self.outcome, Unset):
            outcome = self.outcome

        subscription_id = self.subscription_id

        event_source = self.event_source

        event_type = self.event_type

        failure_code = self.failure_code

        min_age_seconds = self.min_age_seconds

        protect_receipts = self.protect_receipts

        include_non_retryable = self.include_non_retryable

        rate_per_second = self.rate_per_second

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if workflow_runs is not UNSET:
            field_dict["workflow_runs"] = workflow_runs
        if workflow_routing is not UNSET:
            field_dict["workflow_routing"] = workflow_routing
        if parent_job_id is not UNSET:
            field_dict["parent_job_id"] = parent_job_id
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if reason is not UNSET:
            field_dict["reason"] = reason
        if mode is not UNSET:
            field_dict["mode"] = mode
        if outcome is not UNSET:
            field_dict["outcome"] = outcome
        if subscription_id is not UNSET:
            field_dict["subscription_id"] = subscription_id
        if event_source is not UNSET:
            field_dict["event_source"] = event_source
        if event_type is not UNSET:
            field_dict["event_type"] = event_type
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if min_age_seconds is not UNSET:
            field_dict["min_age_seconds"] = min_age_seconds
        if protect_receipts is not UNSET:
            field_dict["protect_receipts"] = protect_receipts
        if include_non_retryable is not UNSET:
            field_dict["include_non_retryable"] = include_non_retryable
        if rate_per_second is not UNSET:
            field_dict["rate_per_second"] = rate_per_second

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_workflow_recovery_plan import EventWorkflowRecoveryPlan
        from ..models.workflow_run_recovery_plan import WorkflowRunRecoveryPlan

        d = dict(src_dict)
        _workflow_runs = d.pop("workflow_runs", UNSET)
        workflow_runs: WorkflowRunRecoveryPlan | Unset
        if isinstance(_workflow_runs, Unset):
            workflow_runs = UNSET
        else:
            workflow_runs = WorkflowRunRecoveryPlan.from_dict(_workflow_runs)

        _workflow_routing = d.pop("workflow_routing", UNSET)
        workflow_routing: EventWorkflowRecoveryPlan | Unset
        if isinstance(_workflow_routing, Unset):
            workflow_routing = UNSET
        else:
            workflow_routing = EventWorkflowRecoveryPlan.from_dict(_workflow_routing)

        _parent_job_id = d.pop("parent_job_id", UNSET)
        parent_job_id: UUID | Unset
        if isinstance(_parent_job_id, Unset):
            parent_job_id = UNSET
        else:
            parent_job_id = UUID(_parent_job_id)

        _request_id = d.pop("request_id", UNSET)
        request_id: UUID | Unset
        if isinstance(_request_id, Unset):
            request_id = UNSET
        else:
            request_id = UUID(_request_id)

        reason = d.pop("reason", UNSET)

        _mode = d.pop("mode", UNSET)
        mode: EventRecoveryRequestMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_event_recovery_request_mode(_mode)

        _outcome = d.pop("outcome", UNSET)
        outcome: EventRecoveryRequestOutcome | Unset
        if isinstance(_outcome, Unset):
            outcome = UNSET
        else:
            outcome = check_event_recovery_request_outcome(_outcome)

        subscription_id = d.pop("subscription_id", UNSET)

        event_source = d.pop("event_source", UNSET)

        event_type = d.pop("event_type", UNSET)

        failure_code = d.pop("failure_code", UNSET)

        min_age_seconds = d.pop("min_age_seconds", UNSET)

        protect_receipts = d.pop("protect_receipts", UNSET)

        include_non_retryable = d.pop("include_non_retryable", UNSET)

        rate_per_second = d.pop("rate_per_second", UNSET)

        event_recovery_request = cls(
            workflow_runs=workflow_runs,
            workflow_routing=workflow_routing,
            parent_job_id=parent_job_id,
            request_id=request_id,
            reason=reason,
            mode=mode,
            outcome=outcome,
            subscription_id=subscription_id,
            event_source=event_source,
            event_type=event_type,
            failure_code=failure_code,
            min_age_seconds=min_age_seconds,
            protect_receipts=protect_receipts,
            include_non_retryable=include_non_retryable,
            rate_per_second=rate_per_second,
        )

        return event_recovery_request
