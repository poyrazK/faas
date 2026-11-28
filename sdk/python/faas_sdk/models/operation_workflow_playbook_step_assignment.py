from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_step_assignment_dependency_review_status import (
    OperationWorkflowPlaybookStepAssignmentDependencyReviewStatus,
    check_operation_workflow_playbook_step_assignment_dependency_review_status,
)
from ..models.operation_workflow_playbook_step_assignment_status import (
    OperationWorkflowPlaybookStepAssignmentStatus,
    check_operation_workflow_playbook_step_assignment_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_progress_item import OperationWorkflowPlaybookProgressItem
    from ..models.operation_workflow_playbook_review_impact import OperationWorkflowPlaybookReviewImpact
    from ..models.operation_workflow_playbook_step_readiness import OperationWorkflowPlaybookStepReadiness
    from ..models.operation_workflow_readiness_assignment_change import OperationWorkflowReadinessAssignmentChange
    from ..models.operation_workflow_readiness_escalation_response import OperationWorkflowReadinessEscalationResponse


T = TypeVar("T", bound="OperationWorkflowPlaybookStepAssignment")


@_attrs_define
class OperationWorkflowPlaybookStepAssignment:
    """OperationWorkflowPlaybookStepAssignment. Assigned step attention in the existing inbox and follow-up queue. Polling
    preserves manual coordination and bounded ownership history.

    """

    progress_id: UUID
    """OperationWorkflowPlaybookStepAssignment.progress_id. Exact pinned checklist identity."""
    outcome_code: str
    """OperationWorkflowPlaybookStepAssignment.outcome_code. Exact outcome follow-up selector."""
    item_index: int
    """OperationWorkflowPlaybookStepAssignment.item_index. Local step index."""
    label: str
    """OperationWorkflowPlaybookStepAssignment.label. Pinned human follow-up label."""
    status: OperationWorkflowPlaybookStepAssignmentStatus
    """OperationWorkflowPlaybookStepAssignment.status. Latest accepted human step review; independent of inbox
    completion."""
    due_at: datetime.datetime
    """OperationWorkflowPlaybookStepAssignment.due_at. Original immutable completion deadline."""
    last_step_owner: str
    """OperationWorkflowPlaybookStepAssignment.last_step_owner. Latest synchronized checklist assignment, distinct
    from a manual queue override."""
    manual_owner: bool
    """OperationWorkflowPlaybookStepAssignment.manual_owner. Manual inbox changes or automatic fallback protect
    queue ownership from later checklist synchronization."""
    history: list[OperationWorkflowReadinessAssignmentChange]
    """OperationWorkflowPlaybookStepAssignment.history. Bounded retained checklist and inbox assignment changes."""
    history_truncated: bool
    """OperationWorkflowPlaybookStepAssignment.history_truncated. Older ownership changes are no longer retained."""
    review_impact: OperationWorkflowPlaybookReviewImpact | Unset = UNSET
    """OperationWorkflowPlaybookReviewImpact. Frozen application guidance explaining the business result protected
    by human review; advisory confidence, independent of reported business state."""
    dependency_review_status: OperationWorkflowPlaybookStepAssignmentDependencyReviewStatus | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignment.dependency_review_status. Latest source checklist decision for this
    review episode, observed by an active watch; independent of inbox acknowledgment or manual completion."""
    dependency_review_prerequisite: str | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignment.dependency_review_prerequisite. Reopened ancestor label captured
    from retained progress history when available; the immutable review revision identifies the event if history has
    been truncated."""
    dependency_review: OperationWorkflowPlaybookProgressItem | Unset = UNSET
    """OperationWorkflowPlaybookProgressItem. Durable advisory checklist coordination, independent of application
    business success."""
    readiness: OperationWorkflowPlaybookStepReadiness | Unset = UNSET
    """OperationWorkflowPlaybookStepReadiness. Human review dependency status, not business state or execution
    authority."""
    source_revision: int | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignment.source_revision. Last synchronized progress revision, used to retain
    intervening owner changes; incomplete source history prevents fallback."""
    response: OperationWorkflowReadinessEscalationResponse | Unset = UNSET
    """Optional acknowledgment response target on a retained assigned escalation. Human acknowledgment fulfills it;
    completion or follow-up clearing stops it. A missed target emits one notice and optional guarded fallback.
    Assignment history retains the latest 16 changes."""

    def to_dict(self) -> dict[str, Any]:
        progress_id = str(self.progress_id)

        outcome_code = self.outcome_code

        item_index = self.item_index

        label = self.label

        status: str = self.status

        due_at = self.due_at.isoformat()

        last_step_owner = self.last_step_owner

        manual_owner = self.manual_owner

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        history_truncated = self.history_truncated

        review_impact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_impact, Unset):
            review_impact = self.review_impact.to_dict()

        dependency_review_status: str | Unset = UNSET
        if not isinstance(self.dependency_review_status, Unset):
            dependency_review_status = self.dependency_review_status

        dependency_review_prerequisite = self.dependency_review_prerequisite

        dependency_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.dependency_review, Unset):
            dependency_review = self.dependency_review.to_dict()

        readiness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.readiness, Unset):
            readiness = self.readiness.to_dict()

        source_revision = self.source_revision

        response: dict[str, Any] | Unset = UNSET
        if not isinstance(self.response, Unset):
            response = self.response.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "progress_id": progress_id,
                "outcome_code": outcome_code,
                "item_index": item_index,
                "label": label,
                "status": status,
                "due_at": due_at,
                "last_step_owner": last_step_owner,
                "manual_owner": manual_owner,
                "history": history,
                "history_truncated": history_truncated,
            }
        )
        if review_impact is not UNSET:
            field_dict["review_impact"] = review_impact
        if dependency_review_status is not UNSET:
            field_dict["dependency_review_status"] = dependency_review_status
        if dependency_review_prerequisite is not UNSET:
            field_dict["dependency_review_prerequisite"] = dependency_review_prerequisite
        if dependency_review is not UNSET:
            field_dict["dependency_review"] = dependency_review
        if readiness is not UNSET:
            field_dict["readiness"] = readiness
        if source_revision is not UNSET:
            field_dict["source_revision"] = source_revision
        if response is not UNSET:
            field_dict["response"] = response

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_progress_item import OperationWorkflowPlaybookProgressItem
        from ..models.operation_workflow_playbook_review_impact import OperationWorkflowPlaybookReviewImpact
        from ..models.operation_workflow_playbook_step_readiness import OperationWorkflowPlaybookStepReadiness
        from ..models.operation_workflow_readiness_assignment_change import OperationWorkflowReadinessAssignmentChange
        from ..models.operation_workflow_readiness_escalation_response import (
            OperationWorkflowReadinessEscalationResponse,
        )

        d = dict(src_dict)
        progress_id = UUID(d.pop("progress_id"))

        outcome_code = d.pop("outcome_code")

        item_index = d.pop("item_index")

        label = d.pop("label")

        status = check_operation_workflow_playbook_step_assignment_status(d.pop("status"))

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        last_step_owner = d.pop("last_step_owner")

        manual_owner = d.pop("manual_owner")

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = OperationWorkflowReadinessAssignmentChange.from_dict(history_item_data)

            history.append(history_item)

        history_truncated = d.pop("history_truncated")

        _review_impact = d.pop("review_impact", UNSET)
        review_impact: OperationWorkflowPlaybookReviewImpact | Unset
        if isinstance(_review_impact, Unset):
            review_impact = UNSET
        else:
            review_impact = OperationWorkflowPlaybookReviewImpact.from_dict(_review_impact)

        _dependency_review_status = d.pop("dependency_review_status", UNSET)
        dependency_review_status: OperationWorkflowPlaybookStepAssignmentDependencyReviewStatus | Unset
        if isinstance(_dependency_review_status, Unset):
            dependency_review_status = UNSET
        else:
            dependency_review_status = check_operation_workflow_playbook_step_assignment_dependency_review_status(
                _dependency_review_status
            )

        dependency_review_prerequisite = d.pop("dependency_review_prerequisite", UNSET)

        _dependency_review = d.pop("dependency_review", UNSET)
        dependency_review: OperationWorkflowPlaybookProgressItem | Unset
        if isinstance(_dependency_review, Unset):
            dependency_review = UNSET
        else:
            dependency_review = OperationWorkflowPlaybookProgressItem.from_dict(_dependency_review)

        _readiness = d.pop("readiness", UNSET)
        readiness: OperationWorkflowPlaybookStepReadiness | Unset
        if isinstance(_readiness, Unset):
            readiness = UNSET
        else:
            readiness = OperationWorkflowPlaybookStepReadiness.from_dict(_readiness)

        source_revision = d.pop("source_revision", UNSET)

        _response = d.pop("response", UNSET)
        response: OperationWorkflowReadinessEscalationResponse | Unset
        if isinstance(_response, Unset):
            response = UNSET
        else:
            response = OperationWorkflowReadinessEscalationResponse.from_dict(_response)

        operation_workflow_playbook_step_assignment = cls(
            progress_id=progress_id,
            outcome_code=outcome_code,
            item_index=item_index,
            label=label,
            status=status,
            due_at=due_at,
            last_step_owner=last_step_owner,
            manual_owner=manual_owner,
            history=history,
            history_truncated=history_truncated,
            review_impact=review_impact,
            dependency_review_status=dependency_review_status,
            dependency_review_prerequisite=dependency_review_prerequisite,
            dependency_review=dependency_review,
            readiness=readiness,
            source_revision=source_revision,
            response=response,
        )

        return operation_workflow_playbook_step_assignment
