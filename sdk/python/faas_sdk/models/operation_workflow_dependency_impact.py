from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_dependent_instance import OperationWorkflowDependentInstance


T = TypeVar("T", bound="OperationWorkflowDependencyImpact")


@_attrs_define
class OperationWorkflowDependencyImpact:
    """One-hop reverse dependency impact within the selected customer/application/environment. Counts cover all current
    retained sources; items are capped at 100 with affected sources first. Omitted when no customer can be identified
    for an unknown account-side prerequisite. Independent of milestone and history pagination.

    """

    items: list[OperationWorkflowDependentInstance]
    workflow_count: int
    impacted_workflow_count: int
    has_more: bool
    """True when the full retained relationship count exceeds the displayed 100 items."""

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        workflow_count = self.workflow_count

        impacted_workflow_count = self.impacted_workflow_count

        has_more = self.has_more

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
                "workflow_count": workflow_count,
                "impacted_workflow_count": impacted_workflow_count,
                "has_more": has_more,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_dependent_instance import OperationWorkflowDependentInstance

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowDependentInstance.from_dict(items_item_data)

            items.append(items_item)

        workflow_count = d.pop("workflow_count")

        impacted_workflow_count = d.pop("impacted_workflow_count")

        has_more = d.pop("has_more")

        operation_workflow_dependency_impact = cls(
            items=items,
            workflow_count=workflow_count,
            impacted_workflow_count=impacted_workflow_count,
            has_more=has_more,
        )

        return operation_workflow_dependency_impact
