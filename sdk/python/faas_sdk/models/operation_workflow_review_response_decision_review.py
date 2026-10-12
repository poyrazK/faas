from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth


T = TypeVar("T", bound="OperationWorkflowReviewResponseDecisionReview")


@_attrs_define
class OperationWorkflowReviewResponseDecisionReview:
    """First changed observation for one explicit response decision. Remains latched until a new explicit decision;
    ordinary edits, acknowledgment and subsequent recovery do not clear it.

    """

    decision_at: datetime.datetime
    """OperationWorkflowReviewResponseDecisionReview.decision_at. Time of the decision that needs revalidation."""
    required_at: datetime.datetime
    """OperationWorkflowReviewResponseDecisionReview.required_at. First sampled condition change, committed with
    its one-time inbox alert."""
    changes: list[str]
    """OperationWorkflowReviewResponseDecisionReview.changes. Differences from the immutable reviewed snapshot,
    including unavailable sources without claiming business failure."""
    health: OperationWorkflowReviewResponseHealth
    """Sampled advisory response-plan condition. Clear means no observed blockers or review obligations, not
    business completion. Sampling stops on response resolution."""
    due_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.due_at. Immutable deadline pinned from required_at and the
    response review window; legacy flags are backfilled."""
    deadline_notified_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseDecisionReview.deadline_notified_at. One-time overdue alert marker;
    acknowledgment and ordinary edits do not fulfill revalidation."""

    def to_dict(self) -> dict[str, Any]:
        decision_at = self.decision_at.isoformat()

        required_at = self.required_at.isoformat()

        changes = self.changes

        health = self.health.to_dict()

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        deadline_notified_at: str | Unset = UNSET
        if not isinstance(self.deadline_notified_at, Unset):
            deadline_notified_at = self.deadline_notified_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "decision_at": decision_at,
                "required_at": required_at,
                "changes": changes,
                "health": health,
            }
        )
        if due_at is not UNSET:
            field_dict["due_at"] = due_at
        if deadline_notified_at is not UNSET:
            field_dict["deadline_notified_at"] = deadline_notified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_health import OperationWorkflowReviewResponseHealth

        d = dict(src_dict)
        decision_at = datetime.datetime.fromisoformat(d.pop("decision_at"))

        required_at = datetime.datetime.fromisoformat(d.pop("required_at"))

        changes = cast(list[str], d.pop("changes"))

        health = OperationWorkflowReviewResponseHealth.from_dict(d.pop("health"))

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        _deadline_notified_at = d.pop("deadline_notified_at", UNSET)
        deadline_notified_at: datetime.datetime | Unset
        if isinstance(_deadline_notified_at, Unset):
            deadline_notified_at = UNSET
        else:
            deadline_notified_at = datetime.datetime.fromisoformat(_deadline_notified_at)

        operation_workflow_review_response_decision_review = cls(
            decision_at=decision_at,
            required_at=required_at,
            changes=changes,
            health=health,
            due_at=due_at,
            deadline_notified_at=deadline_notified_at,
        )

        return operation_workflow_review_response_decision_review
