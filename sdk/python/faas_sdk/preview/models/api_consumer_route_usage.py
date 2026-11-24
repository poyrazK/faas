from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APIConsumerRouteUsage")


@_attrs_define
class APIConsumerRouteUsage:
    """Unweighted billable requests for one route over the requested period."""

    route: str
    """Method and route template. Empty for unattributed requests."""
    billable_requests: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        billable_requests = self.billable_requests

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "billable_requests": billable_requests,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        route = d.pop("route")

        billable_requests = d.pop("billable_requests")

        api_consumer_route_usage = cls(
            route=route,
            billable_requests=billable_requests,
        )

        api_consumer_route_usage.additional_properties = d
        return api_consumer_route_usage

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
