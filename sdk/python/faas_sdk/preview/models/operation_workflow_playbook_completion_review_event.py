from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_completion_review_event_resolution import (
    OperationWorkflowPlaybookCompletionReviewEventResolution,
    check_operation_workflow_playbook_completion_review_event_resolution,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome


T = TypeVar("T", bound="OperationWorkflowPlaybookCompletionReviewEvent")


@_attrs_define
class OperationWorkflowPlaybookCompletionReviewEvent:
    """OperationWorkflowPlaybookCompletionReviewEvent. Durable result change linked to the original closed escalation;
    conveys no application execution authority.

    """

    review_id: UUID
    """OperationWorkflowPlaybookCompletionReviewEvent.review_id. Exact review identity."""
    source_notification_id: UUID
    """OperationWorkflowPlaybookCompletionReviewEvent.source_notification_id. Original closed escalation
    notification."""
    source_sequence: int
    """OperationWorkflowPlaybookCompletionReviewEvent.source_sequence. Linkable original inbox sequence."""
    resolution: OperationWorkflowPlaybookCompletionReviewEventResolution
    """OperationWorkflowPlaybookCompletionReviewEvent.resolution. Original human disposition, unchanged by later
    evidence."""
    previous: OperationWorkflowPlaybookBusinessOutcome
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen
    playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently
    changes the expected outcome."""
    current: OperationWorkflowPlaybookBusinessOutcome
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen
    playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently
    changes the expected outcome."""

    def to_dict(self) -> dict[str, Any]:
        review_id = str(self.review_id)

        source_notification_id = str(self.source_notification_id)

        source_sequence = self.source_sequence

        resolution: str = self.resolution

        previous = self.previous.to_dict()

        current = self.current.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "review_id": review_id,
                "source_notification_id": source_notification_id,
                "source_sequence": source_sequence,
                "resolution": resolution,
                "previous": previous,
                "current": current,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome

        d = dict(src_dict)
        review_id = UUID(d.pop("review_id"))

        source_notification_id = UUID(d.pop("source_notification_id"))

        source_sequence = d.pop("source_sequence")

        resolution = check_operation_workflow_playbook_completion_review_event_resolution(d.pop("resolution"))

        previous = OperationWorkflowPlaybookBusinessOutcome.from_dict(d.pop("previous"))

        current = OperationWorkflowPlaybookBusinessOutcome.from_dict(d.pop("current"))

        operation_workflow_playbook_completion_review_event = cls(
            review_id=review_id,
            source_notification_id=source_notification_id,
            source_sequence=source_sequence,
            resolution=resolution,
            previous=previous,
            current=current,
        )

        return operation_workflow_playbook_completion_review_event
