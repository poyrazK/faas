from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_meter_usage import APIConsumerMeterUsage
    from ..models.api_consumer_route_usage import APIConsumerRouteUsage
    from ..models.consumer_billing_day import ConsumerBillingDay


T = TypeVar("T", bound="ConsumerBillingUsageResponse")


@_attrs_define
class ConsumerBillingUsageResponse:
    """A consumer's daily usage over at most 31 days."""

    since: datetime.datetime
    until: datetime.datetime
    days: list[ConsumerBillingDay]
    route_breakdown: list[APIConsumerRouteUsage] | Unset = UNSET
    meter_breakdown: list[APIConsumerMeterUsage] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since.isoformat()

        until = self.until.isoformat()

        days = []
        for days_item_data in self.days:
            days_item = days_item_data.to_dict()
            days.append(days_item)

        route_breakdown: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.route_breakdown, Unset):
            route_breakdown = []
            for route_breakdown_item_data in self.route_breakdown:
                route_breakdown_item = route_breakdown_item_data.to_dict()
                route_breakdown.append(route_breakdown_item)

        meter_breakdown: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.meter_breakdown, Unset):
            meter_breakdown = []
            for meter_breakdown_item_data in self.meter_breakdown:
                meter_breakdown_item = meter_breakdown_item_data.to_dict()
                meter_breakdown.append(meter_breakdown_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "until": until,
                "days": days,
            }
        )
        if route_breakdown is not UNSET:
            field_dict["route_breakdown"] = route_breakdown
        if meter_breakdown is not UNSET:
            field_dict["meter_breakdown"] = meter_breakdown

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_meter_usage import APIConsumerMeterUsage
        from ..models.api_consumer_route_usage import APIConsumerRouteUsage
        from ..models.consumer_billing_day import ConsumerBillingDay

        d = dict(src_dict)
        since = datetime.datetime.fromisoformat(d.pop("since"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        days = []
        _days = d.pop("days")
        for days_item_data in _days:
            days_item = ConsumerBillingDay.from_dict(days_item_data)

            days.append(days_item)

        _route_breakdown = d.pop("route_breakdown", UNSET)
        route_breakdown: list[APIConsumerRouteUsage] | Unset = UNSET
        if _route_breakdown is not UNSET:
            route_breakdown = []
            for route_breakdown_item_data in _route_breakdown:
                route_breakdown_item = APIConsumerRouteUsage.from_dict(route_breakdown_item_data)

                route_breakdown.append(route_breakdown_item)

        _meter_breakdown = d.pop("meter_breakdown", UNSET)
        meter_breakdown: list[APIConsumerMeterUsage] | Unset = UNSET
        if _meter_breakdown is not UNSET:
            meter_breakdown = []
            for meter_breakdown_item_data in _meter_breakdown:
                meter_breakdown_item = APIConsumerMeterUsage.from_dict(meter_breakdown_item_data)

                meter_breakdown.append(meter_breakdown_item)

        consumer_billing_usage_response = cls(
            since=since,
            until=until,
            days=days,
            route_breakdown=route_breakdown,
            meter_breakdown=meter_breakdown,
        )

        consumer_billing_usage_response.additional_properties = d
        return consumer_billing_usage_response

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
