from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_delivery_response import EventDeliveryResponse
    from ..models.event_fanout_failure_response import EventFanoutFailureResponse


T = TypeVar("T", bound="EventDeliveryListResponse")


@_attrs_define
class EventDeliveryListResponse:
    """App-scoped event invocation and fanout failure history, each ordered newest first."""

    app_slug: str
    deliveries: list[EventDeliveryResponse]
    next_before: str | Unset = UNSET
    """Opaque cursor bound to the app and event identity and state filters."""
    fanout_failures: list[EventFanoutFailureResponse] | Unset = UNSET
    """Terminal recipient routing failures; empty when none exist."""
    next_fanout_before: str | Unset = UNSET
    """Opaque cursor for the next older page of pre-invocation failures."""

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        deliveries = []
        for deliveries_item_data in self.deliveries:
            deliveries_item = deliveries_item_data.to_dict()
            deliveries.append(deliveries_item)

        next_before = self.next_before

        fanout_failures: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.fanout_failures, Unset):
            fanout_failures = []
            for fanout_failures_item_data in self.fanout_failures:
                fanout_failures_item = fanout_failures_item_data.to_dict()
                fanout_failures.append(fanout_failures_item)

        next_fanout_before = self.next_fanout_before

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "deliveries": deliveries,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before
        if fanout_failures is not UNSET:
            field_dict["fanout_failures"] = fanout_failures
        if next_fanout_before is not UNSET:
            field_dict["next_fanout_before"] = next_fanout_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_delivery_response import EventDeliveryResponse
        from ..models.event_fanout_failure_response import EventFanoutFailureResponse

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        deliveries = []
        _deliveries = d.pop("deliveries")
        for deliveries_item_data in _deliveries:
            deliveries_item = EventDeliveryResponse.from_dict(deliveries_item_data)

            deliveries.append(deliveries_item)

        next_before = d.pop("next_before", UNSET)

        _fanout_failures = d.pop("fanout_failures", UNSET)
        fanout_failures: list[EventFanoutFailureResponse] | Unset = UNSET
        if _fanout_failures is not UNSET:
            fanout_failures = []
            for fanout_failures_item_data in _fanout_failures:
                fanout_failures_item = EventFanoutFailureResponse.from_dict(fanout_failures_item_data)

                fanout_failures.append(fanout_failures_item)

        next_fanout_before = d.pop("next_fanout_before", UNSET)

        event_delivery_list_response = cls(
            app_slug=app_slug,
            deliveries=deliveries,
            next_before=next_before,
            fanout_failures=fanout_failures,
            next_fanout_before=next_fanout_before,
        )

        return event_delivery_list_response
