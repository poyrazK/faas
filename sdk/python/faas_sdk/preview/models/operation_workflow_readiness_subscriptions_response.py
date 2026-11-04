from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_subscription import OperationWorkflowReadinessSubscription


T = TypeVar("T", bound="OperationWorkflowReadinessSubscriptionsResponse")


@_attrs_define
class OperationWorkflowReadinessSubscriptionsResponse:
    """Subscriptions in this ownership, application and environment scope."""

    items: list[OperationWorkflowReadinessSubscription]
    """OperationWorkflowReadinessSubscriptionsResponse.items. Subscriptions ordered by creation time and identity."""

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_subscription import OperationWorkflowReadinessSubscription

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowReadinessSubscription.from_dict(items_item_data)

            items.append(items_item)

        operation_workflow_readiness_subscriptions_response = cls(
            items=items,
        )

        return operation_workflow_readiness_subscriptions_response
