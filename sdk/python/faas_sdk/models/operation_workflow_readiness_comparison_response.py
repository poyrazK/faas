from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_change import OperationWorkflowReadinessChange
    from ..models.operation_workflow_readiness_snapshot import OperationWorkflowReadinessSnapshot


T = TypeVar("T", bound="OperationWorkflowReadinessComparisonResponse")


@_attrs_define
class OperationWorkflowReadinessComparisonResponse:
    """Live comparison counts before filtering with bounded matching changes and the next available-edge snapshot."""

    baseline_at: datetime.datetime
    """ReadinessComparisonResponse. Saved snapshot capture time."""
    evaluated_at: datetime.datetime
    """ReadinessComparisonResponse. Time of the shared per-workflow evidence evaluation or comparison completion."""
    baseline_transition_count: int
    """ReadinessComparisonResponse. All transitions in the supplied snapshot before filters."""
    compared_transition_count: int
    """ReadinessComparisonResponse. Transitions with comparable current edges and declared requirements."""
    newly_ready_count: int
    """ReadinessComparisonResponse. Comparable transitions that changed from unmet to ready."""
    newly_blocked_count: int
    """ReadinessComparisonResponse. Comparable transitions that changed from ready to unmet."""
    changed_count: int
    """ReadinessComparisonResponse. Comparable transitions with evidence or reason changes and unchanged readiness
    result."""
    unchanged_count: int
    """ReadinessComparisonResponse. Comparable transitions with unchanged result and evidence summaries."""
    unavailable_count: int
    """ReadinessComparisonResponse. Transitions excluded from readiness comparison due to contract state edge or
    retained-revision boundaries."""
    matching_change_count: int
    """ReadinessComparisonResponse. Changes matching the selected filter before row limit."""
    items: list[OperationWorkflowReadinessChange]
    """ReadinessComparisonResponse. Matching transition comparison rows."""
    snapshot: OperationWorkflowReadinessSnapshot
    """Caller-held compact readiness observations for at most 100 returned transitions. No historical
    authentication or execution authority is implied."""
    has_more: bool
    """ReadinessComparisonResponse. More matching changes exist than the returned row limit."""

    def to_dict(self) -> dict[str, Any]:
        baseline_at = self.baseline_at.isoformat()

        evaluated_at = self.evaluated_at.isoformat()

        baseline_transition_count = self.baseline_transition_count

        compared_transition_count = self.compared_transition_count

        newly_ready_count = self.newly_ready_count

        newly_blocked_count = self.newly_blocked_count

        changed_count = self.changed_count

        unchanged_count = self.unchanged_count

        unavailable_count = self.unavailable_count

        matching_change_count = self.matching_change_count

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        snapshot = self.snapshot.to_dict()

        has_more = self.has_more

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "baseline_at": baseline_at,
                "evaluated_at": evaluated_at,
                "baseline_transition_count": baseline_transition_count,
                "compared_transition_count": compared_transition_count,
                "newly_ready_count": newly_ready_count,
                "newly_blocked_count": newly_blocked_count,
                "changed_count": changed_count,
                "unchanged_count": unchanged_count,
                "unavailable_count": unavailable_count,
                "matching_change_count": matching_change_count,
                "items": items,
                "snapshot": snapshot,
                "has_more": has_more,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_change import OperationWorkflowReadinessChange
        from ..models.operation_workflow_readiness_snapshot import OperationWorkflowReadinessSnapshot

        d = dict(src_dict)
        baseline_at = datetime.datetime.fromisoformat(d.pop("baseline_at"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        baseline_transition_count = d.pop("baseline_transition_count")

        compared_transition_count = d.pop("compared_transition_count")

        newly_ready_count = d.pop("newly_ready_count")

        newly_blocked_count = d.pop("newly_blocked_count")

        changed_count = d.pop("changed_count")

        unchanged_count = d.pop("unchanged_count")

        unavailable_count = d.pop("unavailable_count")

        matching_change_count = d.pop("matching_change_count")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowReadinessChange.from_dict(items_item_data)

            items.append(items_item)

        snapshot = OperationWorkflowReadinessSnapshot.from_dict(d.pop("snapshot"))

        has_more = d.pop("has_more")

        operation_workflow_readiness_comparison_response = cls(
            baseline_at=baseline_at,
            evaluated_at=evaluated_at,
            baseline_transition_count=baseline_transition_count,
            compared_transition_count=compared_transition_count,
            newly_ready_count=newly_ready_count,
            newly_blocked_count=newly_blocked_count,
            changed_count=changed_count,
            unchanged_count=unchanged_count,
            unavailable_count=unavailable_count,
            matching_change_count=matching_change_count,
            items=items,
            snapshot=snapshot,
            has_more=has_more,
        )

        return operation_workflow_readiness_comparison_response
