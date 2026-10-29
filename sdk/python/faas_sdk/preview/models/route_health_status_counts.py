from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RouteHealthStatusCounts")


@_attrs_define
class RouteHealthStatusCounts:
    """Weighted requests and responses for one watched HTTP code in one deployment/window. Rate is responses divided by
    requests, zero when requests are zero.

    """

    requests: int
    responses: int
    rate: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        requests = self.requests

        responses = self.responses

        rate = self.rate

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "requests": requests,
                "responses": responses,
                "rate": rate,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        requests = d.pop("requests")

        responses = d.pop("responses")

        rate = d.pop("rate")

        route_health_status_counts = cls(
            requests=requests,
            responses=responses,
            rate=rate,
        )

        route_health_status_counts.additional_properties = d
        return route_health_status_counts

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
