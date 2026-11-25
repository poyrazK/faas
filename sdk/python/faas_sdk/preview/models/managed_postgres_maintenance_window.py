from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedPostgresMaintenanceWindow")


@_attrs_define
class ManagedPostgresMaintenanceWindow:
    weekday: int
    """UTC weekday (Sunday = 0, Saturday = 6)."""
    start_minute: int
    """Minutes after midnight UTC."""
    duration_minutes: int
    """Length of the maintenance window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        weekday = self.weekday

        start_minute = self.start_minute

        duration_minutes = self.duration_minutes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "weekday": weekday,
                "start_minute": start_minute,
                "duration_minutes": duration_minutes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        weekday = d.pop("weekday")

        start_minute = d.pop("start_minute")

        duration_minutes = d.pop("duration_minutes")

        managed_postgres_maintenance_window = cls(
            weekday=weekday,
            start_minute=start_minute,
            duration_minutes=duration_minutes,
        )

        managed_postgres_maintenance_window.additional_properties = d
        return managed_postgres_maintenance_window

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
