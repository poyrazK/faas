from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_initial_policy_change_preview import WorkflowRecoveryInitialPolicyChangePreview
    from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
    from ..models.workflow_recovery_policy_change_candidate import WorkflowRecoveryPolicyChangeCandidate
    from ..models.workflow_recovery_policy_change_entry import WorkflowRecoveryPolicyChangeEntry
    from ..models.workflow_recovery_policy_response import WorkflowRecoveryPolicyResponse


T = TypeVar("T", bound="WorkflowRecoveryPolicyChangePreview")


@_attrs_define
class WorkflowRecoveryPolicyChangePreview:
    """Consistent read-only snapshot of a proposed full replacement. Budget and cooldown observations retain spent charges
    and preparation targets across versions. Replacement clears automatic pause and failure streak; current contains
    that historical state. Remaining budgets describe proposed settings even when disabled. Serial slots, inspection
    errors and execution quota can still prevent progress. No update is authorized by this response; apply the same
    expected version with deploy write access and MFA. Queue and evidence may change without changing the configuration
    version.

    """

    current: WorkflowRecoveryPolicyResponse
    """Current recovery policy with trusted update actor and scheduler status. A versioned policy replacement
    resets its automatic failure pause and suppresses older pending automatic actions."""
    proposed: WorkflowRecoveryPolicy
    """Explicit app recovery policy. Preview prepares parent-job plans and worker-discovered initial-failure
    reviews. initial_failure requires explicit failure statuses. Opt-in automatic_retry requires saved_failure only;
    automatic_initial requires initial_failure only. Both require explicit workflow and failure-status filters and
    execution limits. Resume count caps the absolute generation; preparation and execution budgets are separate.
    Cancellation remains manual."""
    observed_at: datetime.datetime
    next_version: int
    pending_suppressed: int
    outstanding_retained: int
    entries: list[WorkflowRecoveryPolicyChangeEntry]
    entries_truncated: bool
    automatic_execution_enabled: bool
    hourly_executions: int
    remaining_execution_budget: int
    hourly_prepared_actions: int
    remaining_preparation_budget: int
    inspected_jobs: int
    jobs_truncated: bool
    candidates: list[WorkflowRecoveryPolicyChangeCandidate]
    candidates_truncated: bool
    initial_failures: WorkflowRecoveryInitialPolicyChangePreview | Unset = UNSET
    """Bounded read-only sample of retained generation-zero failures, present for preview policies with
    initial_failure and automatic_initial proposals. Matching workflow and failure-status filters sort first, then
    ascending failure ID; at most five are inspected. Coverage is always partial and counts describe only this
    sample. Candidate preparation and submission simulate processing the sample in order, after sampled parent-job
    preparations; later candidates can hit simulated cooldown or slot pressure. Quota and unresolved fields report
    the current snapshot before simulation. Applying the proposal clears the current pause but retains charges and
    unresolved work. Actual workers rescan and repeat admission checks. No policy, review, cursor, evidence, job or
    notification is written or pruned."""
    execution_cooldown_until: datetime.datetime | Unset = UNSET
    preparation_cooldown_until: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        current = self.current.to_dict()

        proposed = self.proposed.to_dict()

        observed_at = self.observed_at.isoformat()

        next_version = self.next_version

        pending_suppressed = self.pending_suppressed

        outstanding_retained = self.outstanding_retained

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        entries_truncated = self.entries_truncated

        automatic_execution_enabled = self.automatic_execution_enabled

        hourly_executions = self.hourly_executions

        remaining_execution_budget = self.remaining_execution_budget

        hourly_prepared_actions = self.hourly_prepared_actions

        remaining_preparation_budget = self.remaining_preparation_budget

        inspected_jobs = self.inspected_jobs

        jobs_truncated = self.jobs_truncated

        candidates = []
        for candidates_item_data in self.candidates:
            candidates_item = candidates_item_data.to_dict()
            candidates.append(candidates_item)

        candidates_truncated = self.candidates_truncated

        initial_failures: dict[str, Any] | Unset = UNSET
        if not isinstance(self.initial_failures, Unset):
            initial_failures = self.initial_failures.to_dict()

        execution_cooldown_until: str | Unset = UNSET
        if not isinstance(self.execution_cooldown_until, Unset):
            execution_cooldown_until = self.execution_cooldown_until.isoformat()

        preparation_cooldown_until: str | Unset = UNSET
        if not isinstance(self.preparation_cooldown_until, Unset):
            preparation_cooldown_until = self.preparation_cooldown_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "current": current,
                "proposed": proposed,
                "observed_at": observed_at,
                "next_version": next_version,
                "pending_suppressed": pending_suppressed,
                "outstanding_retained": outstanding_retained,
                "entries": entries,
                "entries_truncated": entries_truncated,
                "automatic_execution_enabled": automatic_execution_enabled,
                "hourly_executions": hourly_executions,
                "remaining_execution_budget": remaining_execution_budget,
                "hourly_prepared_actions": hourly_prepared_actions,
                "remaining_preparation_budget": remaining_preparation_budget,
                "inspected_jobs": inspected_jobs,
                "jobs_truncated": jobs_truncated,
                "candidates": candidates,
                "candidates_truncated": candidates_truncated,
            }
        )
        if initial_failures is not UNSET:
            field_dict["initial_failures"] = initial_failures
        if execution_cooldown_until is not UNSET:
            field_dict["execution_cooldown_until"] = execution_cooldown_until
        if preparation_cooldown_until is not UNSET:
            field_dict["preparation_cooldown_until"] = preparation_cooldown_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_initial_policy_change_preview import WorkflowRecoveryInitialPolicyChangePreview
        from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
        from ..models.workflow_recovery_policy_change_candidate import WorkflowRecoveryPolicyChangeCandidate
        from ..models.workflow_recovery_policy_change_entry import WorkflowRecoveryPolicyChangeEntry
        from ..models.workflow_recovery_policy_response import WorkflowRecoveryPolicyResponse

        d = dict(src_dict)
        current = WorkflowRecoveryPolicyResponse.from_dict(d.pop("current"))

        proposed = WorkflowRecoveryPolicy.from_dict(d.pop("proposed"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        next_version = d.pop("next_version")

        pending_suppressed = d.pop("pending_suppressed")

        outstanding_retained = d.pop("outstanding_retained")

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = WorkflowRecoveryPolicyChangeEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        entries_truncated = d.pop("entries_truncated")

        automatic_execution_enabled = d.pop("automatic_execution_enabled")

        hourly_executions = d.pop("hourly_executions")

        remaining_execution_budget = d.pop("remaining_execution_budget")

        hourly_prepared_actions = d.pop("hourly_prepared_actions")

        remaining_preparation_budget = d.pop("remaining_preparation_budget")

        inspected_jobs = d.pop("inspected_jobs")

        jobs_truncated = d.pop("jobs_truncated")

        candidates = []
        _candidates = d.pop("candidates")
        for candidates_item_data in _candidates:
            candidates_item = WorkflowRecoveryPolicyChangeCandidate.from_dict(candidates_item_data)

            candidates.append(candidates_item)

        candidates_truncated = d.pop("candidates_truncated")

        _initial_failures = d.pop("initial_failures", UNSET)
        initial_failures: WorkflowRecoveryInitialPolicyChangePreview | Unset
        if isinstance(_initial_failures, Unset):
            initial_failures = UNSET
        else:
            initial_failures = WorkflowRecoveryInitialPolicyChangePreview.from_dict(_initial_failures)

        _execution_cooldown_until = d.pop("execution_cooldown_until", UNSET)
        execution_cooldown_until: datetime.datetime | Unset
        if isinstance(_execution_cooldown_until, Unset):
            execution_cooldown_until = UNSET
        else:
            execution_cooldown_until = datetime.datetime.fromisoformat(_execution_cooldown_until)

        _preparation_cooldown_until = d.pop("preparation_cooldown_until", UNSET)
        preparation_cooldown_until: datetime.datetime | Unset
        if isinstance(_preparation_cooldown_until, Unset):
            preparation_cooldown_until = UNSET
        else:
            preparation_cooldown_until = datetime.datetime.fromisoformat(_preparation_cooldown_until)

        workflow_recovery_policy_change_preview = cls(
            current=current,
            proposed=proposed,
            observed_at=observed_at,
            next_version=next_version,
            pending_suppressed=pending_suppressed,
            outstanding_retained=outstanding_retained,
            entries=entries,
            entries_truncated=entries_truncated,
            automatic_execution_enabled=automatic_execution_enabled,
            hourly_executions=hourly_executions,
            remaining_execution_budget=remaining_execution_budget,
            hourly_prepared_actions=hourly_prepared_actions,
            remaining_preparation_budget=remaining_preparation_budget,
            inspected_jobs=inspected_jobs,
            jobs_truncated=jobs_truncated,
            candidates=candidates,
            candidates_truncated=candidates_truncated,
            initial_failures=initial_failures,
            execution_cooldown_until=execution_cooldown_until,
            preparation_cooldown_until=preparation_cooldown_until,
        )

        workflow_recovery_policy_change_preview.additional_properties = d
        return workflow_recovery_policy_change_preview

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
