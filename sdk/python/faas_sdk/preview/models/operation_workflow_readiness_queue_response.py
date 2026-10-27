from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_queue_entry import OperationWorkflowReadinessQueueEntry
    from ..models.operation_workflow_readiness_snapshot import OperationWorkflowReadinessSnapshot


T = TypeVar("T", bound="OperationWorkflowReadinessQueueResponse")


@_attrs_define
class OperationWorkflowReadinessQueueResponse:
    """Bounded live readiness queue; counts describe the evaluated sample and filters."""

    snapshot: OperationWorkflowReadinessSnapshot
    """Caller-held compact readiness observations for at most 100 returned transitions. No historical
    authentication or execution authority is implied."""
    items: list[OperationWorkflowReadinessQueueEntry]
    """Matching transitions with unmet rows first."""
    sampled_at: datetime.datetime
    """Time used to select recently updated ongoing workflows."""
    evaluated_at: datetime.datetime
    """Time evaluation completed; reads are live rather than atomic."""
    matching_workflow_count: int
    """Retained ongoing workflows matching app customer and workflow selectors before sampling."""
    sampled_workflow_count: int
    """Ongoing workflows selected up to the workflow cap."""
    excluded_changed_workflow_count: int
    """Sampled workflows excluded after their current report or contract changed."""
    no_transition_workflow_count: int
    """Unchanged sampled workflows without a matching current-state edge."""
    declared_transition_count: int
    """Matching current-state edges before per-workflow edge sampling."""
    evaluated_transition_count: int
    """Current-state edges evaluated before owner and requirement filtering."""
    matching_transition_count: int
    """Evaluated transitions matching filters before the row limit."""
    ready_transition_count: int
    """Matching evaluated transitions with all declared requirements met."""
    unmet_transition_count: int
    """Matching evaluated transitions with unmet declared requirements."""
    workflow_sample_truncated: bool
    """More matching ongoing workflows exist than were sampled."""
    transition_sample_truncated: bool
    """At least one workflow has more matching edges than the per-workflow cap."""
    has_more: bool
    """More matching evaluated rows exist than the response row limit."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        snapshot = self.snapshot.to_dict()

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        sampled_at = self.sampled_at.isoformat()

        evaluated_at = self.evaluated_at.isoformat()

        matching_workflow_count = self.matching_workflow_count

        sampled_workflow_count = self.sampled_workflow_count

        excluded_changed_workflow_count = self.excluded_changed_workflow_count

        no_transition_workflow_count = self.no_transition_workflow_count

        declared_transition_count = self.declared_transition_count

        evaluated_transition_count = self.evaluated_transition_count

        matching_transition_count = self.matching_transition_count

        ready_transition_count = self.ready_transition_count

        unmet_transition_count = self.unmet_transition_count

        workflow_sample_truncated = self.workflow_sample_truncated

        transition_sample_truncated = self.transition_sample_truncated

        has_more = self.has_more

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "snapshot": snapshot,
                "items": items,
                "sampled_at": sampled_at,
                "evaluated_at": evaluated_at,
                "matching_workflow_count": matching_workflow_count,
                "sampled_workflow_count": sampled_workflow_count,
                "excluded_changed_workflow_count": excluded_changed_workflow_count,
                "no_transition_workflow_count": no_transition_workflow_count,
                "declared_transition_count": declared_transition_count,
                "evaluated_transition_count": evaluated_transition_count,
                "matching_transition_count": matching_transition_count,
                "ready_transition_count": ready_transition_count,
                "unmet_transition_count": unmet_transition_count,
                "workflow_sample_truncated": workflow_sample_truncated,
                "transition_sample_truncated": transition_sample_truncated,
                "has_more": has_more,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_queue_entry import OperationWorkflowReadinessQueueEntry
        from ..models.operation_workflow_readiness_snapshot import OperationWorkflowReadinessSnapshot

        d = dict(src_dict)
        snapshot = OperationWorkflowReadinessSnapshot.from_dict(d.pop("snapshot"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowReadinessQueueEntry.from_dict(items_item_data)

            items.append(items_item)

        sampled_at = datetime.datetime.fromisoformat(d.pop("sampled_at"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        matching_workflow_count = d.pop("matching_workflow_count")

        sampled_workflow_count = d.pop("sampled_workflow_count")

        excluded_changed_workflow_count = d.pop("excluded_changed_workflow_count")

        no_transition_workflow_count = d.pop("no_transition_workflow_count")

        declared_transition_count = d.pop("declared_transition_count")

        evaluated_transition_count = d.pop("evaluated_transition_count")

        matching_transition_count = d.pop("matching_transition_count")

        ready_transition_count = d.pop("ready_transition_count")

        unmet_transition_count = d.pop("unmet_transition_count")

        workflow_sample_truncated = d.pop("workflow_sample_truncated")

        transition_sample_truncated = d.pop("transition_sample_truncated")

        has_more = d.pop("has_more")

        operation_workflow_readiness_queue_response = cls(
            snapshot=snapshot,
            items=items,
            sampled_at=sampled_at,
            evaluated_at=evaluated_at,
            matching_workflow_count=matching_workflow_count,
            sampled_workflow_count=sampled_workflow_count,
            excluded_changed_workflow_count=excluded_changed_workflow_count,
            no_transition_workflow_count=no_transition_workflow_count,
            declared_transition_count=declared_transition_count,
            evaluated_transition_count=evaluated_transition_count,
            matching_transition_count=matching_transition_count,
            ready_transition_count=ready_transition_count,
            unmet_transition_count=unmet_transition_count,
            workflow_sample_truncated=workflow_sample_truncated,
            transition_sample_truncated=transition_sample_truncated,
            has_more=has_more,
        )

        operation_workflow_readiness_queue_response.additional_properties = d
        return operation_workflow_readiness_queue_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
