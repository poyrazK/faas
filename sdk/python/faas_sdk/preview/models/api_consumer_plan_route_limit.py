from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="APIConsumerPlanRouteLimit")


@_attrs_define
class APIConsumerPlanRouteLimit:
    """Cap on a consumer's requests on one route. Zero is unlimited; at least one limit is positive. Each admitted request
    counts one, whatever the route's rate-card weight.

    """

    max_requests_per_minute: int | Unset = UNSET
    """Requests per consumer per UTC minute on this route; over the limit the gateway returns 429."""
    max_requests_per_month: int | Unset = UNSET
    """Requests per consumer per UTC month on this route; over the limit the gateway returns 429 until the month
    ends."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_requests_per_minute = self.max_requests_per_minute

        max_requests_per_month = self.max_requests_per_month

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if max_requests_per_minute is not UNSET:
            field_dict["max_requests_per_minute"] = max_requests_per_minute
        if max_requests_per_month is not UNSET:
            field_dict["max_requests_per_month"] = max_requests_per_month

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_requests_per_minute = d.pop("max_requests_per_minute", UNSET)

        max_requests_per_month = d.pop("max_requests_per_month", UNSET)

        api_consumer_plan_route_limit = cls(
            max_requests_per_minute=max_requests_per_minute,
            max_requests_per_month=max_requests_per_month,
        )

        api_consumer_plan_route_limit.additional_properties = d
        return api_consumer_plan_route_limit

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
