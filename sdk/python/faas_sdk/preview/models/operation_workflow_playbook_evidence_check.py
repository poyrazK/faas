from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_evidence_check_status import (
    OperationWorkflowPlaybookEvidenceCheckStatus,
    check_operation_workflow_playbook_evidence_check_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_instance_milestone_ref import OperationWorkflowInstanceMilestoneRef
    from ..models.operation_workflow_playbook_evidence_requirement import OperationWorkflowPlaybookEvidenceRequirement
    from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone


T = TypeVar("T", bound="OperationWorkflowPlaybookEvidenceCheck")


@_attrs_define
class OperationWorkflowPlaybookEvidenceCheck:
    """OperationWorkflowPlaybookEvidenceCheck. Ephemeral case-read observation of a pinned requirement, separate from human
    review and attachment validity. Contract or guidance changes, missing declarations and incomplete coverage remain
    unavailable. Does not change progress or authorize execution.

    """

    requirement: OperationWorkflowPlaybookEvidenceRequirement
    """OperationWorkflowPlaybookEvidenceRequirement. One pinned live application-evidence selector per checklist
    label. Milestone selects a declared operation and milestone. Prerequisite additionally selects an exact current-
    state transition and producer, optionally an alternative group and named path. Verification selects current-
    report blocker resolutions; absent obligations are unavailable."""
    status: OperationWorkflowPlaybookEvidenceCheckStatus
    """OperationWorkflowPlaybookEvidenceCheck.status. Current retained evidence observation, not external proof or
    business success."""
    reason: str
    """OperationWorkflowPlaybookEvidenceCheck.reason. Stable diagnostic code explaining the observation or
    uncertainty."""
    checked_at: datetime.datetime
    """OperationWorkflowPlaybookEvidenceCheck.checked_at. Case evidence observation clock; prerequisite freshness
    retains its original evaluation clock."""
    review_suggested: bool
    """OperationWorkflowPlaybookEvidenceCheck.review_suggested. Completed human item currently has an unsatisfied
    or unavailable requirement; no review is modified."""
    state_revision: int | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceCheck.state_revision. Current report revision when available."""
    reporting_operation_id: UUID | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceCheck.reporting_operation_id. Current application state-report operation."""
    evidence: OperationWorkflowInstanceMilestoneRef | Unset = UNSET
    """Identity and occurrence and publication times of a milestone observed for a declared workflow step."""
    prerequisite: OperationWorkflowRetainedMilestone | Unset = UNSET
    """Verification of a retained app-reported prerequisite fact within the selected ownership boundary, subject,
    workflow instance, and contract version. Verified does not attest to an external provider or authorize business
    writes. Invalidated means an applicable retained fact occurred at or after the prerequisite. Expired means the
    declared maximum evidence age has been reached at evaluation time. Invalidation takes precedence over expiry.
    Awaiting verification means no matching eligible fact is currently retained and does not prove it never
    occurred."""

    def to_dict(self) -> dict[str, Any]:
        requirement = self.requirement.to_dict()

        status: str = self.status

        reason = self.reason

        checked_at = self.checked_at.isoformat()

        review_suggested = self.review_suggested

        state_revision = self.state_revision

        reporting_operation_id: str | Unset = UNSET
        if not isinstance(self.reporting_operation_id, Unset):
            reporting_operation_id = str(self.reporting_operation_id)

        evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evidence, Unset):
            evidence = self.evidence.to_dict()

        prerequisite: dict[str, Any] | Unset = UNSET
        if not isinstance(self.prerequisite, Unset):
            prerequisite = self.prerequisite.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "requirement": requirement,
                "status": status,
                "reason": reason,
                "checked_at": checked_at,
                "review_suggested": review_suggested,
            }
        )
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision
        if reporting_operation_id is not UNSET:
            field_dict["reporting_operation_id"] = reporting_operation_id
        if evidence is not UNSET:
            field_dict["evidence"] = evidence
        if prerequisite is not UNSET:
            field_dict["prerequisite"] = prerequisite

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_instance_milestone_ref import OperationWorkflowInstanceMilestoneRef
        from ..models.operation_workflow_playbook_evidence_requirement import (
            OperationWorkflowPlaybookEvidenceRequirement,
        )
        from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone

        d = dict(src_dict)
        requirement = OperationWorkflowPlaybookEvidenceRequirement.from_dict(d.pop("requirement"))

        status = check_operation_workflow_playbook_evidence_check_status(d.pop("status"))

        reason = d.pop("reason")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        review_suggested = d.pop("review_suggested")

        state_revision = d.pop("state_revision", UNSET)

        _reporting_operation_id = d.pop("reporting_operation_id", UNSET)
        reporting_operation_id: UUID | Unset
        if isinstance(_reporting_operation_id, Unset):
            reporting_operation_id = UNSET
        else:
            reporting_operation_id = UUID(_reporting_operation_id)

        _evidence = d.pop("evidence", UNSET)
        evidence: OperationWorkflowInstanceMilestoneRef | Unset
        if isinstance(_evidence, Unset):
            evidence = UNSET
        else:
            evidence = OperationWorkflowInstanceMilestoneRef.from_dict(_evidence)

        _prerequisite = d.pop("prerequisite", UNSET)
        prerequisite: OperationWorkflowRetainedMilestone | Unset
        if isinstance(_prerequisite, Unset):
            prerequisite = UNSET
        else:
            prerequisite = OperationWorkflowRetainedMilestone.from_dict(_prerequisite)

        operation_workflow_playbook_evidence_check = cls(
            requirement=requirement,
            status=status,
            reason=reason,
            checked_at=checked_at,
            review_suggested=review_suggested,
            state_revision=state_revision,
            reporting_operation_id=reporting_operation_id,
            evidence=evidence,
            prerequisite=prerequisite,
        )

        return operation_workflow_playbook_evidence_check
