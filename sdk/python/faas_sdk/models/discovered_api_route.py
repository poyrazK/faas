from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DiscoveredAPIRoute")


@_attrs_define
class DiscoveredAPIRoute:
    """One durable, replay-safe observed route candidate."""

    route_template: str
    first_seen: datetime.datetime
    last_seen: datetime.datetime
    request_count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route_template = self.route_template

        first_seen = self.first_seen.isoformat()

        last_seen = self.last_seen.isoformat()

        request_count = self.request_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route_template": route_template,
                "first_seen": first_seen,
                "last_seen": last_seen,
                "request_count": request_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        route_template = d.pop("route_template")

        first_seen = datetime.datetime.fromisoformat(d.pop("first_seen"))

        last_seen = datetime.datetime.fromisoformat(d.pop("last_seen"))

        request_count = d.pop("request_count")

        discovered_api_route = cls(
            route_template=route_template,
            first_seen=first_seen,
            last_seen=last_seen,
            request_count=request_count,
        )

        discovered_api_route.additional_properties = d
        return discovered_api_route

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
