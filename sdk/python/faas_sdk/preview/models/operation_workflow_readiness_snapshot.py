from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_snapshot_version import (
    OperationWorkflowReadinessSnapshotVersion,
    check_operation_workflow_readiness_snapshot_version,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry


T = TypeVar("T", bound="OperationWorkflowReadinessSnapshot")


@_attrs_define
class OperationWorkflowReadinessSnapshot:
    """Caller-held compact readiness observations for at most 100 returned transitions. No historical authentication or
    execution authority is implied.

    """

    version: OperationWorkflowReadinessSnapshotVersion
    """ReadinessSnapshot. Snapshot format version; currently 1."""
    query: str
    """ReadinessSnapshot. Account app environment customer-selector and role fingerprint; preserve unchanged when
    saving."""
    captured_at: datetime.datetime
    """ReadinessSnapshot. Time the live snapshot capture completed."""
    items: list[OperationWorkflowReadinessSnapshotEntry]
    """ReadinessSnapshot. Saved transition observations."""

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        query = self.query

        captured_at = self.captured_at.isoformat()

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "query": query,
                "captured_at": captured_at,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry

        d = dict(src_dict)
        version = check_operation_workflow_readiness_snapshot_version(d.pop("version"))

        query = d.pop("query")

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowReadinessSnapshotEntry.from_dict(items_item_data)

            items.append(items_item)

        operation_workflow_readiness_snapshot = cls(
            version=version,
            query=query,
            captured_at=captured_at,
            items=items,
        )

        return operation_workflow_readiness_snapshot
