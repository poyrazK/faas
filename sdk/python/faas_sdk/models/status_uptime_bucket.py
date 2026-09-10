from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="StatusUptimeBucket")


@_attrs_define
class StatusUptimeBucket:
    """Daily public status uptime point."""

    date: datetime.datetime
    uptime_pct: float
    successful: int
    total: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        date = self.date.isoformat()

        uptime_pct = self.uptime_pct

        successful = self.successful

        total = self.total

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "date": date,
                "uptime_pct": uptime_pct,
                "successful": successful,
                "total": total,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        date = datetime.datetime.fromisoformat(d.pop("date"))

        uptime_pct = d.pop("uptime_pct")

        successful = d.pop("successful")

        total = d.pop("total")

        status_uptime_bucket = cls(
            date=date,
            uptime_pct=uptime_pct,
            successful=successful,
            total=total,
        )

        status_uptime_bucket.additional_properties = d
        return status_uptime_bucket

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
