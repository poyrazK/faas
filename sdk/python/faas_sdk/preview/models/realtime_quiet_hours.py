from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RealtimeQuietHours")


@_attrs_define
class RealtimeQuietHours:
    """Daily local-time interval during which push delivery is deferred."""

    timezone: str
    """Named timezone, for example Europe/Rome; Local is rejected."""
    start: str
    end: str

    def to_dict(self) -> dict[str, Any]:
        timezone = self.timezone

        start = self.start

        end = self.end

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "timezone": timezone,
                "start": start,
                "end": end,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        timezone = d.pop("timezone")

        start = d.pop("start")

        end = d.pop("end")

        realtime_quiet_hours = cls(
            timezone=timezone,
            start=start,
            end=end,
        )

        return realtime_quiet_hours
