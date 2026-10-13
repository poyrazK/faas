from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_obligation_fulfillment_review import OperationWorkflowObligationFulfillmentReview


T = TypeVar("T", bound="OperationWorkflowFulfillmentReviewReminder")


@_attrs_define
class OperationWorkflowFulfillmentReviewReminder:
    """Immutable atomic inbox-delivery receipt for one review work-item and deadline. Includes the saved owner, reasons,
    case and evidence context. Identical deadlines do not repeat within the same review cycle; a new deadline or review
    cycle is independently eligible. Delivery does not complete or reopen the original work, change its owner, or assert
    fresh application evidence.

    """

    review_id: str
    notification_id: UUID
    due_at: datetime.datetime
    notified_at: datetime.datetime
    review: OperationWorkflowObligationFulfillmentReview
    """Bounded independent observation of the latest fulfilled case/cycle and its original or reaffirmed proof.
    Flags remain latched until explicit reaffirmation or reopening; normal workflow progress is not invalidation.
    Reasons describe retained coverage, not inferred business failure. Identical semantic observations suppress
    owner notices."""

    def to_dict(self) -> dict[str, Any]:
        review_id = self.review_id

        notification_id = str(self.notification_id)

        due_at = self.due_at.isoformat()

        notified_at = self.notified_at.isoformat()

        review = self.review.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "review_id": review_id,
                "notification_id": notification_id,
                "due_at": due_at,
                "notified_at": notified_at,
                "review": review,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_obligation_fulfillment_review import (
            OperationWorkflowObligationFulfillmentReview,
        )

        d = dict(src_dict)
        review_id = d.pop("review_id")

        notification_id = UUID(d.pop("notification_id"))

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        notified_at = datetime.datetime.fromisoformat(d.pop("notified_at"))

        review = OperationWorkflowObligationFulfillmentReview.from_dict(d.pop("review"))

        operation_workflow_fulfillment_review_reminder = cls(
            review_id=review_id,
            notification_id=notification_id,
            due_at=due_at,
            notified_at=notified_at,
            review=review,
        )

        return operation_workflow_fulfillment_review_reminder
