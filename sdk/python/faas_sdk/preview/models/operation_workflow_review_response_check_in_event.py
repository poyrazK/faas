from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response import OperationWorkflowReviewResponse


T = TypeVar("T", bound="OperationWorkflowReviewResponseCheckInEvent")


@_attrs_define
class OperationWorkflowReviewResponseCheckInEvent:
    """OperationWorkflowReviewResponseCheckInEvent. Frozen response plan, linked reviews and routing at one missed check-
    in, separate from business outcome and alert acknowledgment.

    """

    source_notification_id: UUID
    """OperationWorkflowReviewResponseCheckInEvent.source_notification_id. Original risk alert identity."""
    source_sequence: int
    """OperationWorkflowReviewResponseCheckInEvent.source_sequence. Original alert sequence within this inbox."""
    checked_at: datetime.datetime
    """OperationWorkflowReviewResponseCheckInEvent.checked_at. Periodic evaluation time."""
    check_in_at: datetime.datetime
    """OperationWorkflowReviewResponseCheckInEvent.check_in_at. Original missed response check-in; handoff does not
    reset it."""
    previous_owner: str
    """OperationWorkflowReviewResponseCheckInEvent.previous_owner. Response owner before the overdue check."""
    owner: str
    """OperationWorkflowReviewResponseCheckInEvent.owner. Effective responsible team after guarded fallback."""
    reassigned: bool
    """OperationWorkflowReviewResponseCheckInEvent.reassigned. Whether guarded fallback changed both response
    responsibility and inbox ownership."""
    response: OperationWorkflowReviewResponse
    """OperationWorkflowReviewResponse. Human accountability record under the notification coordination revision
    fence, independent of business workflow success."""

    def to_dict(self) -> dict[str, Any]:
        source_notification_id = str(self.source_notification_id)

        source_sequence = self.source_sequence

        checked_at = self.checked_at.isoformat()

        check_in_at = self.check_in_at.isoformat()

        previous_owner = self.previous_owner

        owner = self.owner

        reassigned = self.reassigned

        response = self.response.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source_notification_id": source_notification_id,
                "source_sequence": source_sequence,
                "checked_at": checked_at,
                "check_in_at": check_in_at,
                "previous_owner": previous_owner,
                "owner": owner,
                "reassigned": reassigned,
                "response": response,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response import OperationWorkflowReviewResponse

        d = dict(src_dict)
        source_notification_id = UUID(d.pop("source_notification_id"))

        source_sequence = d.pop("source_sequence")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        check_in_at = datetime.datetime.fromisoformat(d.pop("check_in_at"))

        previous_owner = d.pop("previous_owner")

        owner = d.pop("owner")

        reassigned = d.pop("reassigned")

        response = OperationWorkflowReviewResponse.from_dict(d.pop("response"))

        operation_workflow_review_response_check_in_event = cls(
            source_notification_id=source_notification_id,
            source_sequence=source_sequence,
            checked_at=checked_at,
            check_in_at=check_in_at,
            previous_owner=previous_owner,
            owner=owner,
            reassigned=reassigned,
            response=response,
        )

        return operation_workflow_review_response_check_in_event
