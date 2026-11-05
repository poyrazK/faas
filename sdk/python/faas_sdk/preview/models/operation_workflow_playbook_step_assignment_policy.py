from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_review_impact import OperationWorkflowPlaybookReviewImpact


T = TypeVar("T", bound="OperationWorkflowPlaybookStepAssignmentPolicy")


@_attrs_define
class OperationWorkflowPlaybookStepAssignmentPolicy:
    """OperationWorkflowPlaybookStepAssignmentPolicy. Pinned team and completion window for one declared outcome follow-up
    step; coordination only.

    """

    label: str
    """OperationWorkflowPlaybookStepAssignmentPolicy.label. Exact unique declared follow-up label."""
    owner: str
    """OperationWorkflowPlaybookStepAssignmentPolicy.owner. Required trimmed control-free UTF-8 team label."""
    complete_after_seconds: int
    """OperationWorkflowPlaybookStepAssignmentPolicy.complete_after_seconds. Completion window from first list
    review for independent steps, or first prerequisite readiness for dependent steps; bounded to seven days."""
    review_impact: OperationWorkflowPlaybookReviewImpact | Unset = UNSET
    """OperationWorkflowPlaybookReviewImpact. Frozen application guidance explaining the business result protected
    by human review; advisory confidence, independent of reported business state."""
    review_after_seconds: int | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignmentPolicy.review_after_seconds. Optional validity review window starting
    at prerequisite reopening; zero omits the target. Pinned independently of the original step completion deadline.
    Inbox acknowledgment does not satisfy this target."""
    review_fallback_owner: str | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignmentPolicy.review_fallback_owner. Optional fallback team after an
    unresolved review deadline; requires a positive review window and a team distinct from owner. Manual assignments
    prevent automatic takeover."""
    acknowledge_after_seconds: int | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignmentPolicy.acknowledge_after_seconds. Optional response window from queue
    delivery; zero disables. Separate from the original step completion deadline."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignmentPolicy.fallback_owner. Optional distinct fallback team; requires a
    positive response window. Assignment changes preserve human ownership history."""

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        owner = self.owner

        complete_after_seconds = self.complete_after_seconds

        review_impact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.review_impact, Unset):
            review_impact = self.review_impact.to_dict()

        review_after_seconds = self.review_after_seconds

        review_fallback_owner = self.review_fallback_owner

        acknowledge_after_seconds = self.acknowledge_after_seconds

        fallback_owner = self.fallback_owner

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "label": label,
                "owner": owner,
                "complete_after_seconds": complete_after_seconds,
            }
        )
        if review_impact is not UNSET:
            field_dict["review_impact"] = review_impact
        if review_after_seconds is not UNSET:
            field_dict["review_after_seconds"] = review_after_seconds
        if review_fallback_owner is not UNSET:
            field_dict["review_fallback_owner"] = review_fallback_owner
        if acknowledge_after_seconds is not UNSET:
            field_dict["acknowledge_after_seconds"] = acknowledge_after_seconds
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_review_impact import OperationWorkflowPlaybookReviewImpact

        d = dict(src_dict)
        label = d.pop("label")

        owner = d.pop("owner")

        complete_after_seconds = d.pop("complete_after_seconds")

        _review_impact = d.pop("review_impact", UNSET)
        review_impact: OperationWorkflowPlaybookReviewImpact | Unset
        if isinstance(_review_impact, Unset):
            review_impact = UNSET
        else:
            review_impact = OperationWorkflowPlaybookReviewImpact.from_dict(_review_impact)

        review_after_seconds = d.pop("review_after_seconds", UNSET)

        review_fallback_owner = d.pop("review_fallback_owner", UNSET)

        acknowledge_after_seconds = d.pop("acknowledge_after_seconds", UNSET)

        fallback_owner = d.pop("fallback_owner", UNSET)

        operation_workflow_playbook_step_assignment_policy = cls(
            label=label,
            owner=owner,
            complete_after_seconds=complete_after_seconds,
            review_impact=review_impact,
            review_after_seconds=review_after_seconds,
            review_fallback_owner=review_fallback_owner,
            acknowledge_after_seconds=acknowledge_after_seconds,
            fallback_owner=fallback_owner,
        )

        return operation_workflow_playbook_step_assignment_policy
