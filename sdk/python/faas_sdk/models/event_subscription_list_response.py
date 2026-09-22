from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_subscription_response import EventSubscriptionResponse


T = TypeVar("T", bound="EventSubscriptionListResponse")


@_attrs_define
class EventSubscriptionListResponse:
    """App-scoped event subscriptions reconciled from the manifest."""

    app_slug: str
    subscriptions: list[EventSubscriptionResponse]

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        subscriptions = []
        for subscriptions_item_data in self.subscriptions:
            subscriptions_item = subscriptions_item_data.to_dict()
            subscriptions.append(subscriptions_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "subscriptions": subscriptions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_response import EventSubscriptionResponse

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        subscriptions = []
        _subscriptions = d.pop("subscriptions")
        for subscriptions_item_data in _subscriptions:
            subscriptions_item = EventSubscriptionResponse.from_dict(subscriptions_item_data)

            subscriptions.append(subscriptions_item)

        event_subscription_list_response = cls(
            app_slug=app_slug,
            subscriptions=subscriptions,
        )

        return event_subscription_list_response
