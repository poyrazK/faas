from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RouteAwakeWhatIf")


@_attrs_define
class RouteAwakeWhatIf:
    """Estimated change had this route's requests never reached the app, with every other request unchanged."""

    awake_seconds_saved: int
    gb_hours_saved: float
    wakes_change: int
    """Change in cold wakes; positive when other routes would find the app parked more often."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        awake_seconds_saved = self.awake_seconds_saved

        gb_hours_saved = self.gb_hours_saved

        wakes_change = self.wakes_change

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "awake_seconds_saved": awake_seconds_saved,
                "gb_hours_saved": gb_hours_saved,
                "wakes_change": wakes_change,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        awake_seconds_saved = d.pop("awake_seconds_saved")

        gb_hours_saved = d.pop("gb_hours_saved")

        wakes_change = d.pop("wakes_change")

        route_awake_what_if = cls(
            awake_seconds_saved=awake_seconds_saved,
            gb_hours_saved=gb_hours_saved,
            wakes_change=wakes_change,
        )

        route_awake_what_if.additional_properties = d
        return route_awake_what_if

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
