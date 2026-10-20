from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventStorageUsageResponseLimits")


@_attrs_define
class EventStorageUsageResponseLimits:
    """
    Example:
        {'retained_events': 16384, 'retained_bytes': 67108864}

    """

    retained_events: int
    retained_bytes: int

    def to_dict(self) -> dict[str, Any]:
        retained_events = self.retained_events

        retained_bytes = self.retained_bytes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "retained_events": retained_events,
                "retained_bytes": retained_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        retained_events = d.pop("retained_events")

        retained_bytes = d.pop("retained_bytes")

        event_storage_usage_response_limits = cls(
            retained_events=retained_events,
            retained_bytes=retained_bytes,
        )

        return event_storage_usage_response_limits
