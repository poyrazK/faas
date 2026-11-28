from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_freeze_weekly_days_item import ReleaseFreezeWeeklyDaysItem, check_release_freeze_weekly_days_item

T = TypeVar("T", bound="ReleaseFreezeWeekly")


@_attrs_define
class ReleaseFreezeWeekly:
    """A weekly freeze window: it opens on each listed day at `start` in `time_zone` and lasts `duration_minutes`, so it
    may run past midnight or into the next week.

    """

    days: list[ReleaseFreezeWeeklyDaysItem]
    """Weekdays the window opens on."""
    start: str
    """Local opening time, HH:MM."""
    duration_minutes: int
    """How long each window lasts."""
    time_zone: str
    """IANA time zone, such as Europe/Istanbul or UTC."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        days = []
        for days_item_data in self.days:
            days_item: str = days_item_data
            days.append(days_item)

        start = self.start

        duration_minutes = self.duration_minutes

        time_zone = self.time_zone

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "days": days,
                "start": start,
                "duration_minutes": duration_minutes,
                "time_zone": time_zone,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        days = []
        _days = d.pop("days")
        for days_item_data in _days:
            days_item = check_release_freeze_weekly_days_item(days_item_data)

            days.append(days_item)

        start = d.pop("start")

        duration_minutes = d.pop("duration_minutes")

        time_zone = d.pop("time_zone")

        release_freeze_weekly = cls(
            days=days,
            start=start,
            duration_minutes=duration_minutes,
            time_zone=time_zone,
        )

        release_freeze_weekly.additional_properties = d
        return release_freeze_weekly

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
