from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventStorageUsageResponseLimits")


@_attrs_define
class EventStorageUsageResponseLimits:
    """
    Example:
        {'retained_events': 131072, 'retained_bytes': 268435456, 'settled_retention_seconds': 259200}

    """

    retained_events: int
    retained_bytes: int
    settled_retention_seconds: int | Unset = UNSET
    """How long a settled receipt is kept on the plan; it bounds duplicate acceptance, recovery and replay ranges.
    Absent from servers that predate per-plan windows."""

    def to_dict(self) -> dict[str, Any]:
        retained_events = self.retained_events

        retained_bytes = self.retained_bytes

        settled_retention_seconds = self.settled_retention_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "retained_events": retained_events,
                "retained_bytes": retained_bytes,
            }
        )
        if settled_retention_seconds is not UNSET:
            field_dict["settled_retention_seconds"] = settled_retention_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        retained_events = d.pop("retained_events")

        retained_bytes = d.pop("retained_bytes")

        settled_retention_seconds = d.pop("settled_retention_seconds", UNSET)

        event_storage_usage_response_limits = cls(
            retained_events=retained_events,
            retained_bytes=retained_bytes,
            settled_retention_seconds=settled_retention_seconds,
        )

        return event_storage_usage_response_limits
