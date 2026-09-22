from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_delivery_response import EventDeliveryResponse


T = TypeVar("T", bound="EventDeliveryListResponse")


@_attrs_define
class EventDeliveryListResponse:
    """App-scoped event delivery page, ordered newest first."""

    app_slug: str
    deliveries: list[EventDeliveryResponse]
    next_before: str | Unset = UNSET
    """ID cursor for the next older page."""

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        deliveries = []
        for deliveries_item_data in self.deliveries:
            deliveries_item = deliveries_item_data.to_dict()
            deliveries.append(deliveries_item)

        next_before = self.next_before

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "deliveries": deliveries,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_delivery_response import EventDeliveryResponse

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        deliveries = []
        _deliveries = d.pop("deliveries")
        for deliveries_item_data in _deliveries:
            deliveries_item = EventDeliveryResponse.from_dict(deliveries_item_data)

            deliveries.append(deliveries_item)

        next_before = d.pop("next_before", UNSET)

        event_delivery_list_response = cls(
            app_slug=app_slug,
            deliveries=deliveries,
            next_before=next_before,
        )

        return event_delivery_list_response
