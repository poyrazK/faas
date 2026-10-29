from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry
    from ..models.operation_workflow_readiness_verification_change import OperationWorkflowReadinessVerificationChange


T = TypeVar("T", bound="OperationWorkflowReadinessVerificationEvent")


@_attrs_define
class OperationWorkflowReadinessVerificationEvent:
    """Durable in-app notification linked to its original completion watch. Source describes the original notification
    edge; verified does not imply workflow readiness or execution authority.

    """

    watch_id: UUID
    """OperationWorkflowReadinessVerificationEvent.watch_id. Server-managed advisory verification observation for
    the retained source completion."""
    source_notification_id: UUID
    """OperationWorkflowReadinessVerificationEvent.source_notification_id. Server-managed advisory verification
    observation for the retained source completion."""
    source_sequence: int
    """OperationWorkflowReadinessVerificationEvent.source_sequence. Server-managed advisory verification
    observation for the retained source completion."""
    checked_at: datetime.datetime
    """OperationWorkflowReadinessVerificationEvent.checked_at. Server-managed advisory verification observation for
    the retained source completion."""
    source: OperationWorkflowReadinessSnapshotEntry
    """One saved transition identity and stable evidence summaries for comparison within the same ownership scope."""
    changes: list[OperationWorkflowReadinessVerificationChange]
    """OperationWorkflowReadinessVerificationEvent.changes. Server-managed advisory verification observation for
    the retained source completion."""

    def to_dict(self) -> dict[str, Any]:
        watch_id = str(self.watch_id)

        source_notification_id = str(self.source_notification_id)

        source_sequence = self.source_sequence

        checked_at = self.checked_at.isoformat()

        source = self.source.to_dict()

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "watch_id": watch_id,
                "source_notification_id": source_notification_id,
                "source_sequence": source_sequence,
                "checked_at": checked_at,
                "source": source,
                "changes": changes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry
        from ..models.operation_workflow_readiness_verification_change import (
            OperationWorkflowReadinessVerificationChange,
        )

        d = dict(src_dict)
        watch_id = UUID(d.pop("watch_id"))

        source_notification_id = UUID(d.pop("source_notification_id"))

        source_sequence = d.pop("source_sequence")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        source = OperationWorkflowReadinessSnapshotEntry.from_dict(d.pop("source"))

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = OperationWorkflowReadinessVerificationChange.from_dict(changes_item_data)

            changes.append(changes_item)

        operation_workflow_readiness_verification_event = cls(
            watch_id=watch_id,
            source_notification_id=source_notification_id,
            source_sequence=source_sequence,
            checked_at=checked_at,
            source=source,
            changes=changes,
        )

        return operation_workflow_readiness_verification_event
