from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.profile_periodic_monitor import ProfilePeriodicMonitor


T = TypeVar("T", bound="ListProfilePeriodicMonitorsResponse")


@_attrs_define
class ListProfilePeriodicMonitorsResponse:
    """Current periodic profiling monitors for the authenticated application."""

    monitors: list[ProfilePeriodicMonitor]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        monitors = []
        for monitors_item_data in self.monitors:
            monitors_item = monitors_item_data.to_dict()
            monitors.append(monitors_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "monitors": monitors,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_periodic_monitor import ProfilePeriodicMonitor

        d = dict(src_dict)
        monitors = []
        _monitors = d.pop("monitors")
        for monitors_item_data in _monitors:
            monitors_item = ProfilePeriodicMonitor.from_dict(monitors_item_data)

            monitors.append(monitors_item)

        list_profile_periodic_monitors_response = cls(
            monitors=monitors,
        )

        list_profile_periodic_monitors_response.additional_properties = d
        return list_profile_periodic_monitors_response

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
