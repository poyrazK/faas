from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_handoff_reassignment import (
        OperationWorkflowReviewResponseHandoffReassignment,
    )


T = TypeVar("T", bound="OperationWorkflowReviewResponseHandoffReassignmentEvent")


@_attrs_define
class OperationWorkflowReviewResponseHandoffReassignmentEvent:
    """Historical handoff notice to the assigned team. Check current ownership before accepting; the original review
    deadline remains unchanged.

    """

    source_notification_id: UUID
    """OperationWorkflowReviewResponseHandoffReassignmentEvent.source_notification_id. Original response alert
    identity."""
    source_sequence: int
    """OperationWorkflowReviewResponseHandoffReassignmentEvent.source_sequence. Original response sequence in this
    inbox."""
    reassignment: OperationWorkflowReviewResponseHandoffReassignment
    """Immutable manual routing record for an authorized operator, with preserved decline context and one-time
    notification intent. Ownership assignment is not acceptance."""

    def to_dict(self) -> dict[str, Any]:
        source_notification_id = str(self.source_notification_id)

        source_sequence = self.source_sequence

        reassignment = self.reassignment.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source_notification_id": source_notification_id,
                "source_sequence": source_sequence,
                "reassignment": reassignment,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_handoff_reassignment import (
            OperationWorkflowReviewResponseHandoffReassignment,
        )

        d = dict(src_dict)
        source_notification_id = UUID(d.pop("source_notification_id"))

        source_sequence = d.pop("source_sequence")

        reassignment = OperationWorkflowReviewResponseHandoffReassignment.from_dict(d.pop("reassignment"))

        operation_workflow_review_response_handoff_reassignment_event = cls(
            source_notification_id=source_notification_id,
            source_sequence=source_sequence,
            reassignment=reassignment,
        )

        return operation_workflow_review_response_handoff_reassignment_event
