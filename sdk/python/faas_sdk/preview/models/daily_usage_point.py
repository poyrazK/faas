from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DailyUsagePoint")


@_attrs_define
class DailyUsagePoint:
    """One day in the account's trailing 30 UTC calendar day usage trend (issue #308)."""

    date: datetime.date
    """UTC calendar day."""
    gb_hours: float
    """Account-wide GB-hours consumed on this day. Informational; uses the usage summary conversion."""
    top_app_slug: str | Unset = UNSET
    """Slug of the app contributing the most GB-hours on this day."""
    top_app_gb_hours: float | Unset = UNSET
    """GB-hours consumed by top_app_slug on this day."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        date = self.date.isoformat()

        gb_hours = self.gb_hours

        top_app_slug = self.top_app_slug

        top_app_gb_hours = self.top_app_gb_hours

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "date": date,
                "gb_hours": gb_hours,
            }
        )
        if top_app_slug is not UNSET:
            field_dict["top_app_slug"] = top_app_slug
        if top_app_gb_hours is not UNSET:
            field_dict["top_app_gb_hours"] = top_app_gb_hours

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        date = datetime.date.fromisoformat(d.pop("date"))

        gb_hours = d.pop("gb_hours")

        top_app_slug = d.pop("top_app_slug", UNSET)

        top_app_gb_hours = d.pop("top_app_gb_hours", UNSET)

        daily_usage_point = cls(
            date=date,
            gb_hours=gb_hours,
            top_app_slug=top_app_slug,
            top_app_gb_hours=top_app_gb_hours,
        )

        daily_usage_point.additional_properties = d
        return daily_usage_point

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
