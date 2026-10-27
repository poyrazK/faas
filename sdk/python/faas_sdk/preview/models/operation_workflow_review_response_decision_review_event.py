from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_decision import OperationWorkflowReviewResponseDecision
    from ..models.operation_workflow_review_response_decision_review import (
        OperationWorkflowReviewResponseDecisionReview,
    )


T = TypeVar("T", bound="OperationWorkflowReviewResponseDecisionReviewEvent")


@_attrs_define
class OperationWorkflowReviewResponseDecisionReviewEvent:
    """One durable revalidation alert per explicit decision, routed to the current response owner without changing intent,
    deadlines or application execution.

    """

    source_notification_id: UUID
    """OperationWorkflowReviewResponseDecisionReviewEvent.source_notification_id. Original risk alert identity."""
    source_sequence: int
    """OperationWorkflowReviewResponseDecisionReviewEvent.source_sequence. Original alert sequence in this inbox."""
    decision: OperationWorkflowReviewResponseDecision
    """Immutable human decision with accountable actor and the reviewed health snapshot. Application execution
    remains application-owned."""
    review: OperationWorkflowReviewResponseDecisionReview
    """First changed observation for one explicit response decision. Remains latched until a new explicit decision;
    ordinary edits, acknowledgment and subsequent recovery do not clear it."""

    def to_dict(self) -> dict[str, Any]:
        source_notification_id = str(self.source_notification_id)

        source_sequence = self.source_sequence

        decision = self.decision.to_dict()

        review = self.review.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source_notification_id": source_notification_id,
                "source_sequence": source_sequence,
                "decision": decision,
                "review": review,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_decision import OperationWorkflowReviewResponseDecision
        from ..models.operation_workflow_review_response_decision_review import (
            OperationWorkflowReviewResponseDecisionReview,
        )

        d = dict(src_dict)
        source_notification_id = UUID(d.pop("source_notification_id"))

        source_sequence = d.pop("source_sequence")

        decision = OperationWorkflowReviewResponseDecision.from_dict(d.pop("decision"))

        review = OperationWorkflowReviewResponseDecisionReview.from_dict(d.pop("review"))

        operation_workflow_review_response_decision_review_event = cls(
            source_notification_id=source_notification_id,
            source_sequence=source_sequence,
            decision=decision,
            review=review,
        )

        return operation_workflow_review_response_decision_review_event
