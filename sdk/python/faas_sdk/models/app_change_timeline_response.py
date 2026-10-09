from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_change_timeline_response_unavailable_sources_item import (
    AppChangeTimelineResponseUnavailableSourcesItem,
    check_app_change_timeline_response_unavailable_sources_item,
)

if TYPE_CHECKING:
    from ..models.app_change_event import AppChangeEvent


T = TypeVar("T", bound="AppChangeTimelineResponse")


@_attrs_define
class AppChangeTimelineResponse:
    """ADR-741 change timeline for one app over [since, until), newest first."""

    app_id: str
    app_slug: str
    since: datetime.datetime
    until: datetime.datetime
    events: list[AppChangeEvent]
    truncated: bool
    """Older events in the window were dropped at the 200-event cap."""
    unavailable_sources: list[AppChangeTimelineResponseUnavailableSourcesItem]
    """Sources that could not be read; their events are missing, not absent."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        app_slug = self.app_slug

        since = self.since.isoformat()

        until = self.until.isoformat()

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        truncated = self.truncated

        unavailable_sources = []
        for unavailable_sources_item_data in self.unavailable_sources:
            unavailable_sources_item: str = unavailable_sources_item_data
            unavailable_sources.append(unavailable_sources_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "app_slug": app_slug,
                "since": since,
                "until": until,
                "events": events,
                "truncated": truncated,
                "unavailable_sources": unavailable_sources,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_change_event import AppChangeEvent

        d = dict(src_dict)
        app_id = d.pop("app_id")

        app_slug = d.pop("app_slug")

        since = datetime.datetime.fromisoformat(d.pop("since"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = AppChangeEvent.from_dict(events_item_data)

            events.append(events_item)

        truncated = d.pop("truncated")

        unavailable_sources = []
        _unavailable_sources = d.pop("unavailable_sources")
        for unavailable_sources_item_data in _unavailable_sources:
            unavailable_sources_item = check_app_change_timeline_response_unavailable_sources_item(
                unavailable_sources_item_data
            )

            unavailable_sources.append(unavailable_sources_item)

        app_change_timeline_response = cls(
            app_id=app_id,
            app_slug=app_slug,
            since=since,
            until=until,
            events=events,
            truncated=truncated,
            unavailable_sources=unavailable_sources,
        )

        app_change_timeline_response.additional_properties = d
        return app_change_timeline_response

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
