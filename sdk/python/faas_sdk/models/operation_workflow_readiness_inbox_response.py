from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_notification import OperationWorkflowReadinessNotification
    from ..models.operation_workflow_readiness_subscription import OperationWorkflowReadinessSubscription


T = TypeVar("T", bound="OperationWorkflowReadinessInboxResponse")


@_attrs_define
class OperationWorkflowReadinessInboxResponse:
    """Retained notification page with explicit cursor gaps; unchanged evaluations do not create entries."""

    subscription: OperationWorkflowReadinessSubscription
    """Durable subscription configuration and most recent successful bounded evaluation; deletion cancels future
    evaluations and removes its inbox."""
    items: list[OperationWorkflowReadinessNotification]
    """OperationWorkflowReadinessInboxResponse.items. Notifications after the supplied sequence in ascending order."""
    latest_sequence: int
    """OperationWorkflowReadinessInboxResponse.latest_sequence. Most recent committed subscription sequence."""
    next_sequence: int
    """OperationWorkflowReadinessInboxResponse.next_sequence. Cursor for the next page."""
    resync_required: bool
    """OperationWorkflowReadinessInboxResponse.resync_required. The supplied cursor is outside retained history;
    the page starts at retained entries without claiming completeness."""
    has_more: bool
    """OperationWorkflowReadinessInboxResponse.has_more. More committed entries follow this page."""

    def to_dict(self) -> dict[str, Any]:
        subscription = self.subscription.to_dict()

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        latest_sequence = self.latest_sequence

        next_sequence = self.next_sequence

        resync_required = self.resync_required

        has_more = self.has_more

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription": subscription,
                "items": items,
                "latest_sequence": latest_sequence,
                "next_sequence": next_sequence,
                "resync_required": resync_required,
                "has_more": has_more,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_notification import OperationWorkflowReadinessNotification
        from ..models.operation_workflow_readiness_subscription import OperationWorkflowReadinessSubscription

        d = dict(src_dict)
        subscription = OperationWorkflowReadinessSubscription.from_dict(d.pop("subscription"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowReadinessNotification.from_dict(items_item_data)

            items.append(items_item)

        latest_sequence = d.pop("latest_sequence")

        next_sequence = d.pop("next_sequence")

        resync_required = d.pop("resync_required")

        has_more = d.pop("has_more")

        operation_workflow_readiness_inbox_response = cls(
            subscription=subscription,
            items=items,
            latest_sequence=latest_sequence,
            next_sequence=next_sequence,
            resync_required=resync_required,
            has_more=has_more,
        )

        return operation_workflow_readiness_inbox_response
