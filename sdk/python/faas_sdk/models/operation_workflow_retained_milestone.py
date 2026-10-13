from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_retained_milestone_status import (
    OperationWorkflowRetainedMilestoneStatus,
    check_operation_workflow_retained_milestone_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_instance_milestone_ref import OperationWorkflowInstanceMilestoneRef
    from ..models.operation_workflow_prerequisite_recovery import OperationWorkflowPrerequisiteRecovery
    from ..models.operation_workflow_retained_milestone_freshness import OperationWorkflowRetainedMilestoneFreshness
    from ..models.operation_workflow_retained_milestone_invalidation import (
        OperationWorkflowRetainedMilestoneInvalidation,
    )
    from ..models.operation_workflow_retained_milestone_requirement import OperationWorkflowRetainedMilestoneRequirement


T = TypeVar("T", bound="OperationWorkflowRetainedMilestone")


@_attrs_define
class OperationWorkflowRetainedMilestone:
    """Verification of a retained app-reported prerequisite fact within the selected ownership boundary, subject, workflow
    instance, and contract version. Verified does not attest to an external provider or authorize business writes.
    Invalidated means an applicable retained fact occurred at or after the prerequisite. Expired means the declared
    maximum evidence age has been reached at evaluation time. Invalidation takes precedence over expiry. Awaiting
    verification means no matching eligible fact is currently retained and does not prove it never occurred.

    """

    requirement: OperationWorkflowRetainedMilestoneRequirement
    """Source operation and milestone bound to a declared transaction-backed step of the selected workflow
    contract."""
    status: OperationWorkflowRetainedMilestoneStatus
    recovery: OperationWorkflowPrerequisiteRecovery | Unset = UNSET
    """Application-owned steps to restore a missing or unusable retained prerequisite. Derived from the pinned
    selector; absent for verified evidence. This is guidance and does not establish that the producer operation is
    currently executable. Steps are ordered perform_operation then publish_milestone then recheck_readiness.
    Publication must use the same ownership boundary and workflow instance and contract version. Freshness limits
    still apply."""
    freshness: OperationWorkflowRetainedMilestoneFreshness | Unset = UNSET
    """Evidence age evaluated at the shared server timestamp. Expiry is inclusive; missing evidence omits expiry
    and age fields. Remaining seconds round up while eligible and are zero after expiry."""
    invalidation: OperationWorkflowRetainedMilestoneInvalidation | Unset = UNSET
    """Identity of the retained fact that invalidated a prerequisite by business occurrence time. Equal occurrence
    timestamps invalidate conservatively; a later prerequisite fact can satisfy the requirement again. Normal
    retention applies to both sources."""
    evidence: OperationWorkflowInstanceMilestoneRef | Unset = UNSET
    """Identity and occurrence and publication times of a milestone observed for a declared workflow step."""

    def to_dict(self) -> dict[str, Any]:
        requirement = self.requirement.to_dict()

        status: str = self.status

        recovery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery, Unset):
            recovery = self.recovery.to_dict()

        freshness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.freshness, Unset):
            freshness = self.freshness.to_dict()

        invalidation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.invalidation, Unset):
            invalidation = self.invalidation.to_dict()

        evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evidence, Unset):
            evidence = self.evidence.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "requirement": requirement,
                "status": status,
            }
        )
        if recovery is not UNSET:
            field_dict["recovery"] = recovery
        if freshness is not UNSET:
            field_dict["freshness"] = freshness
        if invalidation is not UNSET:
            field_dict["invalidation"] = invalidation
        if evidence is not UNSET:
            field_dict["evidence"] = evidence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_instance_milestone_ref import OperationWorkflowInstanceMilestoneRef
        from ..models.operation_workflow_prerequisite_recovery import OperationWorkflowPrerequisiteRecovery
        from ..models.operation_workflow_retained_milestone_freshness import OperationWorkflowRetainedMilestoneFreshness
        from ..models.operation_workflow_retained_milestone_invalidation import (
            OperationWorkflowRetainedMilestoneInvalidation,
        )
        from ..models.operation_workflow_retained_milestone_requirement import (
            OperationWorkflowRetainedMilestoneRequirement,
        )

        d = dict(src_dict)
        requirement = OperationWorkflowRetainedMilestoneRequirement.from_dict(d.pop("requirement"))

        status = check_operation_workflow_retained_milestone_status(d.pop("status"))

        _recovery = d.pop("recovery", UNSET)
        recovery: OperationWorkflowPrerequisiteRecovery | Unset
        if isinstance(_recovery, Unset):
            recovery = UNSET
        else:
            recovery = OperationWorkflowPrerequisiteRecovery.from_dict(_recovery)

        _freshness = d.pop("freshness", UNSET)
        freshness: OperationWorkflowRetainedMilestoneFreshness | Unset
        if isinstance(_freshness, Unset):
            freshness = UNSET
        else:
            freshness = OperationWorkflowRetainedMilestoneFreshness.from_dict(_freshness)

        _invalidation = d.pop("invalidation", UNSET)
        invalidation: OperationWorkflowRetainedMilestoneInvalidation | Unset
        if isinstance(_invalidation, Unset):
            invalidation = UNSET
        else:
            invalidation = OperationWorkflowRetainedMilestoneInvalidation.from_dict(_invalidation)

        _evidence = d.pop("evidence", UNSET)
        evidence: OperationWorkflowInstanceMilestoneRef | Unset
        if isinstance(_evidence, Unset):
            evidence = UNSET
        else:
            evidence = OperationWorkflowInstanceMilestoneRef.from_dict(_evidence)

        operation_workflow_retained_milestone = cls(
            requirement=requirement,
            status=status,
            recovery=recovery,
            freshness=freshness,
            invalidation=invalidation,
            evidence=evidence,
        )

        return operation_workflow_retained_milestone
