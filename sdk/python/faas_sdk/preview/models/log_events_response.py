from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.log_query_event import LogQueryEvent


T = TypeVar("T", bound="LogEventsResponse")


@_attrs_define
class LogEventsResponse:
    """One newest-first page of an app's log ledger (ADR-20261018083536)."""

    since: datetime.datetime
    """Effective window start after clamping to retention."""
    until: datetime.datetime
    events: list[LogQueryEvent]
    next_cursor: str | Unset = UNSET
    """Present when more events match; pass it back as cursor."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since.isoformat()

        until = self.until.isoformat()

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "until": until,
                "events": events,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.log_query_event import LogQueryEvent

        d = dict(src_dict)
        since = datetime.datetime.fromisoformat(d.pop("since"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = LogQueryEvent.from_dict(events_item_data)

            events.append(events_item)

        next_cursor = d.pop("next_cursor", UNSET)

        log_events_response = cls(
            since=since,
            until=until,
            events=events,
            next_cursor=next_cursor,
        )

        log_events_response.additional_properties = d
        return log_events_response

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
