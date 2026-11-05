from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry
    from ..models.operation_workflow_condition_refresh_priority import OperationWorkflowConditionRefreshPriority
    from ..models.operation_workflow_decision_outcome_evidence import OperationWorkflowDecisionOutcomeEvidence
    from ..models.operation_workflow_gap_queue_priority import OperationWorkflowGapQueuePriority
    from ..models.operation_workflow_new_missing_condition_progress_instance import (
        OperationWorkflowNewMissingConditionProgressInstance,
    )
    from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification
    from ..models.operation_workflow_state import OperationWorkflowState


T = TypeVar("T", bound="OperationWorkflowPerformanceInstance")


@_attrs_define
class OperationWorkflowPerformanceInstance:
    """Current state and verification findings for one complete-history contributor. Customer responses omit tenant
    identifiers. Observed seconds measure the selected historical group rather than current ownership alone.

    """

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    operation_id: UUID
    state: OperationWorkflowState
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    observed_seconds: int
    resolution_verifications: list[OperationWorkflowResolutionVerification]
    awaiting_verification_count: int
    resolution_verification_count: int
    gap_queue_priority: OperationWorkflowGapQueuePriority | Unset = UNSET
    """All current reporting gaps before queue filters and evidence previews. Guidance contains every eligible
    pinned missing expectation, including owners beyond the evidence preview; it carries no prior report facts.
    Blocked intervals spanning contracts are omitted."""
    gap_progress: OperationWorkflowNewMissingConditionProgressInstance | Unset = UNSET
    """One current ongoing workflow with missing reports and a comparable retained baseline, ordered by stable
    workflow identity. Baseline_report identifies the pinned baseline definition source. A terminal baseline has no
    active expectations; newly active current gaps are new_expectation rather than reporting loss. Conditions use
    canonical current step and condition ID order. Customer projections omit tenant identifiers."""
    missing_condition_count: int | Unset = UNSET
    """Exact missing condition count for the selected current state and refresh owner before preview limits."""
    missing_conditions_truncated: bool | Unset = UNSET
    missing_conditions: list[OperationWorkflowConditionCoverageEntry] | Unset = UNSET
    """First matching missing conditions ordered by workflow step then condition ID with latest reporting Operation
    pinned expectations and public guidance. No qualifying report exists in current contexts, so evidence is absent.
   """
    refresh_priority: OperationWorkflowConditionRefreshPriority | Unset = UNSET
    """Evidence and explanation for one ongoing workflow with overdue condition reports. Rankings use the same
    token-bound sample, complete-history eligibility and pinned definitions as active condition coverage. Owner
    lists and unassigned condition counts include all qualifying identities before the 16-fact preview cap. The
    oldest evidence has the earliest selected overdue report timestamp with canonical identity breaking ties, and is
    included independently of the preview. This is observational prioritization without business severity or
    execution authority."""
    refresh_evidence_count: int | Unset = UNSET
    """Exact number of selected overdue conditions for this contributor. Owner dimensions restrict the count to
    that owner; workflow priority dimensions count all overdue conditions across owners."""
    refresh_evidence_truncated: bool | Unset = UNSET
    refresh_evidence: list[OperationWorkflowDecisionOutcomeEvidence] | Unset = UNSET
    """First 16 matching overdue condition facts in canonical identity order with pinned refresh guidance and
    token-bound deadlines. More conditions may qualify; refresh_evidence_count remains exact."""
    decision_evidence: OperationWorkflowDecisionOutcomeEvidence | Unset = UNSET
    """Retained report classifying this contributor. Completed outcome groups use the earliest qualifying fact;
    active condition groups use the latest matching fact among current decision contexts. The optional business
    decision snapshot references its fact within operation_id. A path without a decision is evidenced by the report
    itself. These are application-reported facts and do not establish causation."""
    platform_tenant_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        operation_id = str(self.operation_id)

        state = self.state.to_dict()

        observed_seconds = self.observed_seconds

        resolution_verifications = []
        for resolution_verifications_item_data in self.resolution_verifications:
            resolution_verifications_item = resolution_verifications_item_data.to_dict()
            resolution_verifications.append(resolution_verifications_item)

        awaiting_verification_count = self.awaiting_verification_count

        resolution_verification_count = self.resolution_verification_count

        gap_queue_priority: dict[str, Any] | Unset = UNSET
        if not isinstance(self.gap_queue_priority, Unset):
            gap_queue_priority = self.gap_queue_priority.to_dict()

        gap_progress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.gap_progress, Unset):
            gap_progress = self.gap_progress.to_dict()

        missing_condition_count = self.missing_condition_count

        missing_conditions_truncated = self.missing_conditions_truncated

        missing_conditions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.missing_conditions, Unset):
            missing_conditions = []
            for missing_conditions_item_data in self.missing_conditions:
                missing_conditions_item = missing_conditions_item_data.to_dict()
                missing_conditions.append(missing_conditions_item)

        refresh_priority: dict[str, Any] | Unset = UNSET
        if not isinstance(self.refresh_priority, Unset):
            refresh_priority = self.refresh_priority.to_dict()

        refresh_evidence_count = self.refresh_evidence_count

        refresh_evidence_truncated = self.refresh_evidence_truncated

        refresh_evidence: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.refresh_evidence, Unset):
            refresh_evidence = []
            for refresh_evidence_item_data in self.refresh_evidence:
                refresh_evidence_item = refresh_evidence_item_data.to_dict()
                refresh_evidence.append(refresh_evidence_item)

        decision_evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision_evidence, Unset):
            decision_evidence = self.decision_evidence.to_dict()

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "operation_id": operation_id,
                "state": state,
                "observed_seconds": observed_seconds,
                "resolution_verifications": resolution_verifications,
                "awaiting_verification_count": awaiting_verification_count,
                "resolution_verification_count": resolution_verification_count,
            }
        )
        if gap_queue_priority is not UNSET:
            field_dict["gap_queue_priority"] = gap_queue_priority
        if gap_progress is not UNSET:
            field_dict["gap_progress"] = gap_progress
        if missing_condition_count is not UNSET:
            field_dict["missing_condition_count"] = missing_condition_count
        if missing_conditions_truncated is not UNSET:
            field_dict["missing_conditions_truncated"] = missing_conditions_truncated
        if missing_conditions is not UNSET:
            field_dict["missing_conditions"] = missing_conditions
        if refresh_priority is not UNSET:
            field_dict["refresh_priority"] = refresh_priority
        if refresh_evidence_count is not UNSET:
            field_dict["refresh_evidence_count"] = refresh_evidence_count
        if refresh_evidence_truncated is not UNSET:
            field_dict["refresh_evidence_truncated"] = refresh_evidence_truncated
        if refresh_evidence is not UNSET:
            field_dict["refresh_evidence"] = refresh_evidence
        if decision_evidence is not UNSET:
            field_dict["decision_evidence"] = decision_evidence
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_condition_coverage_entry import OperationWorkflowConditionCoverageEntry
        from ..models.operation_workflow_condition_refresh_priority import OperationWorkflowConditionRefreshPriority
        from ..models.operation_workflow_decision_outcome_evidence import OperationWorkflowDecisionOutcomeEvidence
        from ..models.operation_workflow_gap_queue_priority import OperationWorkflowGapQueuePriority
        from ..models.operation_workflow_new_missing_condition_progress_instance import (
            OperationWorkflowNewMissingConditionProgressInstance,
        )
        from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification
        from ..models.operation_workflow_state import OperationWorkflowState

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        operation_id = UUID(d.pop("operation_id"))

        state = OperationWorkflowState.from_dict(d.pop("state"))

        observed_seconds = d.pop("observed_seconds")

        resolution_verifications = []
        _resolution_verifications = d.pop("resolution_verifications")
        for resolution_verifications_item_data in _resolution_verifications:
            resolution_verifications_item = OperationWorkflowResolutionVerification.from_dict(
                resolution_verifications_item_data
            )

            resolution_verifications.append(resolution_verifications_item)

        awaiting_verification_count = d.pop("awaiting_verification_count")

        resolution_verification_count = d.pop("resolution_verification_count")

        _gap_queue_priority = d.pop("gap_queue_priority", UNSET)
        gap_queue_priority: OperationWorkflowGapQueuePriority | Unset
        if isinstance(_gap_queue_priority, Unset):
            gap_queue_priority = UNSET
        else:
            gap_queue_priority = OperationWorkflowGapQueuePriority.from_dict(_gap_queue_priority)

        _gap_progress = d.pop("gap_progress", UNSET)
        gap_progress: OperationWorkflowNewMissingConditionProgressInstance | Unset
        if isinstance(_gap_progress, Unset):
            gap_progress = UNSET
        else:
            gap_progress = OperationWorkflowNewMissingConditionProgressInstance.from_dict(_gap_progress)

        missing_condition_count = d.pop("missing_condition_count", UNSET)

        missing_conditions_truncated = d.pop("missing_conditions_truncated", UNSET)

        _missing_conditions = d.pop("missing_conditions", UNSET)
        missing_conditions: list[OperationWorkflowConditionCoverageEntry] | Unset = UNSET
        if _missing_conditions is not UNSET:
            missing_conditions = []
            for missing_conditions_item_data in _missing_conditions:
                missing_conditions_item = OperationWorkflowConditionCoverageEntry.from_dict(
                    missing_conditions_item_data
                )

                missing_conditions.append(missing_conditions_item)

        _refresh_priority = d.pop("refresh_priority", UNSET)
        refresh_priority: OperationWorkflowConditionRefreshPriority | Unset
        if isinstance(_refresh_priority, Unset):
            refresh_priority = UNSET
        else:
            refresh_priority = OperationWorkflowConditionRefreshPriority.from_dict(_refresh_priority)

        refresh_evidence_count = d.pop("refresh_evidence_count", UNSET)

        refresh_evidence_truncated = d.pop("refresh_evidence_truncated", UNSET)

        _refresh_evidence = d.pop("refresh_evidence", UNSET)
        refresh_evidence: list[OperationWorkflowDecisionOutcomeEvidence] | Unset = UNSET
        if _refresh_evidence is not UNSET:
            refresh_evidence = []
            for refresh_evidence_item_data in _refresh_evidence:
                refresh_evidence_item = OperationWorkflowDecisionOutcomeEvidence.from_dict(refresh_evidence_item_data)

                refresh_evidence.append(refresh_evidence_item)

        _decision_evidence = d.pop("decision_evidence", UNSET)
        decision_evidence: OperationWorkflowDecisionOutcomeEvidence | Unset
        if isinstance(_decision_evidence, Unset):
            decision_evidence = UNSET
        else:
            decision_evidence = OperationWorkflowDecisionOutcomeEvidence.from_dict(_decision_evidence)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_performance_instance = cls(
            subject=subject,
            operation_id=operation_id,
            state=state,
            observed_seconds=observed_seconds,
            resolution_verifications=resolution_verifications,
            awaiting_verification_count=awaiting_verification_count,
            resolution_verification_count=resolution_verification_count,
            gap_queue_priority=gap_queue_priority,
            gap_progress=gap_progress,
            missing_condition_count=missing_condition_count,
            missing_conditions_truncated=missing_conditions_truncated,
            missing_conditions=missing_conditions,
            refresh_priority=refresh_priority,
            refresh_evidence_count=refresh_evidence_count,
            refresh_evidence_truncated=refresh_evidence_truncated,
            refresh_evidence=refresh_evidence,
            decision_evidence=decision_evidence,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_performance_instance
