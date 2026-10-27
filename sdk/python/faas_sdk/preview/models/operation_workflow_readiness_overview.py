from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness


T = TypeVar("T", bound="OperationWorkflowReadinessOverview")


@_attrs_define
class OperationWorkflowReadinessOverview:
    """At most 100 current-state declared edges evaluated without planned milestones. Counts cover all declared current-
    state edges; history cursors do not paginate this overview.

    """

    items: list[OperationWorkflowTransitionReadiness]
    transition_count: int
    has_more: bool

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        transition_count = self.transition_count

        has_more = self.has_more

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
                "transition_count": transition_count,
                "has_more": has_more,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowTransitionReadiness.from_dict(items_item_data)

            items.append(items_item)

        transition_count = d.pop("transition_count")

        has_more = d.pop("has_more")

        operation_workflow_readiness_overview = cls(
            items=items,
            transition_count=transition_count,
            has_more=has_more,
        )

        return operation_workflow_readiness_overview
