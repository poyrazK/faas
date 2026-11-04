from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_obligation_fulfillment_review import OperationWorkflowObligationFulfillmentReview


T = TypeVar("T", bound="OperationWorkflowFulfillmentReviewEscalation")


@_attrs_define
class OperationWorkflowFulfillmentReviewEscalation:
    """Immutable destination delivery receipt for an unresolved review after its actual overdue reminder and configured
    grace period. Reassignment after that reminder cancels escalation for that deadline, including reverting an owner
    change; a new deadline starts a new window. Reaffirmation, reopening, supersession and changed policy/deadline
    suppress stale delivery. Delivery preserves review ownership and original fulfillment and does not execute
    application work.

    """

    review_id: str
    notification_id: UUID
    reminder_notification_id: UUID
    due_at: datetime.datetime
    escalate_at: datetime.datetime
    notified_at: datetime.datetime
    escalation_owner: str
    review: OperationWorkflowObligationFulfillmentReview
    """Bounded independent observation of the latest fulfilled case/cycle and its original or reaffirmed proof.
    Flags remain latched until explicit reaffirmation or reopening; normal workflow progress is not invalidation.
    Reasons describe retained coverage, not inferred business failure. Identical semantic observations suppress
    owner notices."""

    def to_dict(self) -> dict[str, Any]:
        review_id = self.review_id

        notification_id = str(self.notification_id)

        reminder_notification_id = str(self.reminder_notification_id)

        due_at = self.due_at.isoformat()

        escalate_at = self.escalate_at.isoformat()

        notified_at = self.notified_at.isoformat()

        escalation_owner = self.escalation_owner

        review = self.review.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "review_id": review_id,
                "notification_id": notification_id,
                "reminder_notification_id": reminder_notification_id,
                "due_at": due_at,
                "escalate_at": escalate_at,
                "notified_at": notified_at,
                "escalation_owner": escalation_owner,
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

        reminder_notification_id = UUID(d.pop("reminder_notification_id"))

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        escalate_at = datetime.datetime.fromisoformat(d.pop("escalate_at"))

        notified_at = datetime.datetime.fromisoformat(d.pop("notified_at"))

        escalation_owner = d.pop("escalation_owner")

        review = OperationWorkflowObligationFulfillmentReview.from_dict(d.pop("review"))

        operation_workflow_fulfillment_review_escalation = cls(
            review_id=review_id,
            notification_id=notification_id,
            reminder_notification_id=reminder_notification_id,
            due_at=due_at,
            escalate_at=escalate_at,
            notified_at=notified_at,
            escalation_owner=escalation_owner,
            review=review,
        )

        return operation_workflow_fulfillment_review_escalation
