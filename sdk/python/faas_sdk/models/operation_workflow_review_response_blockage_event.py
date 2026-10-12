from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReviewResponseBlockageEvent")


@_attrs_define
class OperationWorkflowReviewResponseBlockageEvent:
    """Sampled blocker changes and positively verified recoveries for a linked response plan. Recovery does not resolve the
    response or establish a business result.

    """

    source_notification_id: UUID
    """OperationWorkflowReviewResponseBlockageEvent.source_notification_id. Original response alert identity."""
    source_sequence: int
    """OperationWorkflowReviewResponseBlockageEvent.source_sequence. Original response alert sequence within this
    inbox."""
    checked_at: datetime.datetime
    """OperationWorkflowReviewResponseBlockageEvent.checked_at. Case observation time."""
    added_blockers: list[str]
    """OperationWorkflowReviewResponseBlockageEvent.added_blockers. Newly observed blockers labeled by fixed linked
    review position; first observations are silent."""
    cleared_blockers: list[str] | Unset = UNSET
    """OperationWorkflowReviewResponseBlockageEvent.cleared_blockers. Previously observed blockers positively
    verified as cleared; unavailable sources and changed failure reasons cannot establish recovery."""

    def to_dict(self) -> dict[str, Any]:
        source_notification_id = str(self.source_notification_id)

        source_sequence = self.source_sequence

        checked_at = self.checked_at.isoformat()

        added_blockers = self.added_blockers

        cleared_blockers: list[str] | Unset = UNSET
        if not isinstance(self.cleared_blockers, Unset):
            cleared_blockers = self.cleared_blockers

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source_notification_id": source_notification_id,
                "source_sequence": source_sequence,
                "checked_at": checked_at,
                "added_blockers": added_blockers,
            }
        )
        if cleared_blockers is not UNSET:
            field_dict["cleared_blockers"] = cleared_blockers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_notification_id = UUID(d.pop("source_notification_id"))

        source_sequence = d.pop("source_sequence")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        added_blockers = cast(list[str], d.pop("added_blockers"))

        cleared_blockers = cast(list[str], d.pop("cleared_blockers", UNSET))

        operation_workflow_review_response_blockage_event = cls(
            source_notification_id=source_notification_id,
            source_sequence=source_sequence,
            checked_at=checked_at,
            added_blockers=added_blockers,
            cleared_blockers=cleared_blockers,
        )

        return operation_workflow_review_response_blockage_event
