from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_handoff_decline import (
        OperationWorkflowReviewResponseHandoffDecline,
    )


T = TypeVar("T", bound="OperationWorkflowReviewResponseHandoffDeclineEvent")


@_attrs_define
class OperationWorkflowReviewResponseHandoffDeclineEvent:
    """One historical decline notification routed to the previous owner, without automatic reassignment or deadline
    changes. Consult current response ownership before acting.

    """

    source_notification_id: UUID
    """OperationWorkflowReviewResponseHandoffDeclineEvent.source_notification_id. Original response alert identity."""
    source_sequence: int
    """OperationWorkflowReviewResponseHandoffDeclineEvent.source_sequence. Original response sequence within this
    inbox."""
    decline: OperationWorkflowReviewResponseHandoffDecline
    """Immutable decline for a responsible team recorded by an authenticated authorized operator, without proof of
    team membership. Retained history carries its one-time notification intent."""

    def to_dict(self) -> dict[str, Any]:
        source_notification_id = str(self.source_notification_id)

        source_sequence = self.source_sequence

        decline = self.decline.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source_notification_id": source_notification_id,
                "source_sequence": source_sequence,
                "decline": decline,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_handoff_decline import (
            OperationWorkflowReviewResponseHandoffDecline,
        )

        d = dict(src_dict)
        source_notification_id = UUID(d.pop("source_notification_id"))

        source_sequence = d.pop("source_sequence")

        decline = OperationWorkflowReviewResponseHandoffDecline.from_dict(d.pop("decline"))

        operation_workflow_review_response_handoff_decline_event = cls(
            source_notification_id=source_notification_id,
            source_sequence=source_sequence,
            decline=decline,
        )

        return operation_workflow_review_response_handoff_decline_event
