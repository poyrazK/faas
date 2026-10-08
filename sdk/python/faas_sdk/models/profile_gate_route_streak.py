from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_gate_route_streak_status import (
    ProfileGateRouteStreakStatus,
    check_profile_gate_route_streak_status,
)

T = TypeVar("T", bound="ProfileGateRouteStreak")


@_attrs_define
class ProfileGateRouteStreak:
    route: str
    status: ProfileGateRouteStreakStatus
    count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        status: str = self.status

        count = self.count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "status": status,
                "count": count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        route = d.pop("route")

        status = check_profile_gate_route_streak_status(d.pop("status"))

        count = d.pop("count")

        profile_gate_route_streak = cls(
            route=route,
            status=status,
            count=count,
        )

        profile_gate_route_streak.additional_properties = d
        return profile_gate_route_streak

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
