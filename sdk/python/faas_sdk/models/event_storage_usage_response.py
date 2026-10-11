from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_storage_usage_response_limits import EventStorageUsageResponseLimits


T = TypeVar("T", bound="EventStorageUsageResponse")


@_attrs_define
class EventStorageUsageResponse:
    """Retained customer event counts, logical JSON bytes, pending age and current account budgets.

    Example:
        {'retained_events': 0, 'retained_bytes': 0, 'pending_events': 0, 'oldest_pending_at': None, 'limits':
            {'retained_events': 16384, 'retained_bytes': 67108864}}

    """

    retained_events: int
    retained_bytes: int
    pending_events: int
    oldest_pending_at: datetime.datetime | None
    limits: EventStorageUsageResponseLimits

    def to_dict(self) -> dict[str, Any]:
        retained_events = self.retained_events

        retained_bytes = self.retained_bytes

        pending_events = self.pending_events

        oldest_pending_at: None | str
        if isinstance(self.oldest_pending_at, datetime.datetime):
            oldest_pending_at = self.oldest_pending_at.isoformat()
        else:
            oldest_pending_at = self.oldest_pending_at

        limits = self.limits.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "retained_events": retained_events,
                "retained_bytes": retained_bytes,
                "pending_events": pending_events,
                "oldest_pending_at": oldest_pending_at,
                "limits": limits,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_storage_usage_response_limits import EventStorageUsageResponseLimits

        d = dict(src_dict)
        retained_events = d.pop("retained_events")

        retained_bytes = d.pop("retained_bytes")

        pending_events = d.pop("pending_events")

        def _parse_oldest_pending_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                oldest_pending_at_type_0 = datetime.datetime.fromisoformat(data)

                return oldest_pending_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        oldest_pending_at = _parse_oldest_pending_at(d.pop("oldest_pending_at"))

        limits = EventStorageUsageResponseLimits.from_dict(d.pop("limits"))

        event_storage_usage_response = cls(
            retained_events=retained_events,
            retained_bytes=retained_bytes,
            pending_events=pending_events,
            oldest_pending_at=oldest_pending_at,
            limits=limits,
        )

        return event_storage_usage_response
