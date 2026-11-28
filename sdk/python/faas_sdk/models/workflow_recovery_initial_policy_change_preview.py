from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.workflow_recovery_initial_policy_change_preview_coverage import (
    WorkflowRecoveryInitialPolicyChangePreviewCoverage,
    check_workflow_recovery_initial_policy_change_preview_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_initial_policy_change_candidate import WorkflowRecoveryInitialPolicyChangeCandidate


T = TypeVar("T", bound="WorkflowRecoveryInitialPolicyChangePreview")


@_attrs_define
class WorkflowRecoveryInitialPolicyChangePreview:
    """Bounded read-only sample of retained generation-zero failures, present for preview policies with initial_failure and
    automatic_initial proposals. Matching workflow and failure-status filters sort first, then ascending failure ID; at
    most five are inspected. Coverage is always partial and counts describe only this sample. Candidate preparation and
    submission simulate processing the sample in order, after sampled parent-job preparations; later candidates can hit
    simulated cooldown or slot pressure. Quota and unresolved fields report the current snapshot before simulation.
    Applying the proposal clears the current pause but retains charges and unresolved work. Actual workers rescan and
    repeat admission checks. No policy, review, cursor, evidence, job or notification is written or pruned.

    """

    coverage: WorkflowRecoveryInitialPolicyChangePreviewCoverage
    inspected_failures: int
    truncated: bool
    eligible: int
    """Sampled failures passing policy filters and fresh safety diagnostics; preparation or execution pressure can
    still block them."""
    excluded: int
    """Sampled failures excluded by policy settings, including disabled proposals."""
    safety_blocked: int
    """Sampled policy-matching failures blocked by live safety or changed evidence."""
    would_prepare: int
    would_submit: int
    retained_reviews: int
    """Initial reviews that count after normal history pruning, plus unresolved automatic submissions retained
    indefinitely. Preview does not perform that pruning."""
    remaining_review_capacity: int
    active_recovery_jobs: int
    """Active native or routing recovery jobs across the account, including paused jobs."""
    remaining_recovery_job_capacity: int
    unresolved_recovery: bool
    """Current app has unresolved automatic or native recovery work that blocks a new automatic first submission."""
    candidates: list[WorkflowRecoveryInitialPolicyChangeCandidate]
    runtime_available: bool | Unset = UNSET
    """HTTP server runtime availability. When false, automatic submission decisions report
    workflow_runtime_disabled."""

    def to_dict(self) -> dict[str, Any]:
        coverage: str = self.coverage

        inspected_failures = self.inspected_failures

        truncated = self.truncated

        eligible = self.eligible

        excluded = self.excluded

        safety_blocked = self.safety_blocked

        would_prepare = self.would_prepare

        would_submit = self.would_submit

        retained_reviews = self.retained_reviews

        remaining_review_capacity = self.remaining_review_capacity

        active_recovery_jobs = self.active_recovery_jobs

        remaining_recovery_job_capacity = self.remaining_recovery_job_capacity

        unresolved_recovery = self.unresolved_recovery

        candidates = []
        for candidates_item_data in self.candidates:
            candidates_item = candidates_item_data.to_dict()
            candidates.append(candidates_item)

        runtime_available = self.runtime_available

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "coverage": coverage,
                "inspected_failures": inspected_failures,
                "truncated": truncated,
                "eligible": eligible,
                "excluded": excluded,
                "safety_blocked": safety_blocked,
                "would_prepare": would_prepare,
                "would_submit": would_submit,
                "retained_reviews": retained_reviews,
                "remaining_review_capacity": remaining_review_capacity,
                "active_recovery_jobs": active_recovery_jobs,
                "remaining_recovery_job_capacity": remaining_recovery_job_capacity,
                "unresolved_recovery": unresolved_recovery,
                "candidates": candidates,
            }
        )
        if runtime_available is not UNSET:
            field_dict["runtime_available"] = runtime_available

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_initial_policy_change_candidate import (
            WorkflowRecoveryInitialPolicyChangeCandidate,
        )

        d = dict(src_dict)
        coverage = check_workflow_recovery_initial_policy_change_preview_coverage(d.pop("coverage"))

        inspected_failures = d.pop("inspected_failures")

        truncated = d.pop("truncated")

        eligible = d.pop("eligible")

        excluded = d.pop("excluded")

        safety_blocked = d.pop("safety_blocked")

        would_prepare = d.pop("would_prepare")

        would_submit = d.pop("would_submit")

        retained_reviews = d.pop("retained_reviews")

        remaining_review_capacity = d.pop("remaining_review_capacity")

        active_recovery_jobs = d.pop("active_recovery_jobs")

        remaining_recovery_job_capacity = d.pop("remaining_recovery_job_capacity")

        unresolved_recovery = d.pop("unresolved_recovery")

        candidates = []
        _candidates = d.pop("candidates")
        for candidates_item_data in _candidates:
            candidates_item = WorkflowRecoveryInitialPolicyChangeCandidate.from_dict(candidates_item_data)

            candidates.append(candidates_item)

        runtime_available = d.pop("runtime_available", UNSET)

        workflow_recovery_initial_policy_change_preview = cls(
            coverage=coverage,
            inspected_failures=inspected_failures,
            truncated=truncated,
            eligible=eligible,
            excluded=excluded,
            safety_blocked=safety_blocked,
            would_prepare=would_prepare,
            would_submit=would_submit,
            retained_reviews=retained_reviews,
            remaining_review_capacity=remaining_review_capacity,
            active_recovery_jobs=active_recovery_jobs,
            remaining_recovery_job_capacity=remaining_recovery_job_capacity,
            unresolved_recovery=unresolved_recovery,
            candidates=candidates,
            runtime_available=runtime_available,
        )

        return workflow_recovery_initial_policy_change_preview
