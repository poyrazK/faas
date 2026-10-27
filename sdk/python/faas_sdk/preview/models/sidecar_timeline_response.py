from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.sidecar_timeline_status import SidecarTimelineStatus
    from ..models.wake_timeline_event import WakeTimelineEvent


T = TypeVar("T", bound="SidecarTimelineResponse")


@_attrs_define
class SidecarTimelineResponse:
    """Envelope for GET /v1/apps/{slug}/sidecars/{sidecar_name}/timeline."""

    sidecar_name: str
    """Echo of the sidecar_name path segment."""
    app_id: UUID
    """Identifier of the app that owns this sidecar timeline (resolved from the slug)."""
    events: list[WakeTimelineEvent]
    limit: int
    """Number of sidecar frames returned, from 1 through 1000."""
    latest: SidecarTimelineStatus | Unset = UNSET
    """Most recent health transition for a sidecar, if one has been recorded."""
    next_cursor: str | Unset = UNSET
    """RFC 3339 timestamp cursor for the next sidecar page; empty when no more frames remain."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sidecar_name = self.sidecar_name

        app_id = str(self.app_id)

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        limit = self.limit

        latest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.latest, Unset):
            latest = self.latest.to_dict()

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sidecar_name": sidecar_name,
                "app_id": app_id,
                "events": events,
                "limit": limit,
            }
        )
        if latest is not UNSET:
            field_dict["latest"] = latest
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.sidecar_timeline_status import SidecarTimelineStatus
        from ..models.wake_timeline_event import WakeTimelineEvent

        d = dict(src_dict)
        sidecar_name = d.pop("sidecar_name")

        app_id = UUID(d.pop("app_id"))

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = WakeTimelineEvent.from_dict(events_item_data)

            events.append(events_item)

        limit = d.pop("limit")

        _latest = d.pop("latest", UNSET)
        latest: SidecarTimelineStatus | Unset
        if isinstance(_latest, Unset):
            latest = UNSET
        else:
            latest = SidecarTimelineStatus.from_dict(_latest)

        next_cursor = d.pop("next_cursor", UNSET)

        sidecar_timeline_response = cls(
            sidecar_name=sidecar_name,
            app_id=app_id,
            events=events,
            limit=limit,
            latest=latest,
            next_cursor=next_cursor,
        )

        sidecar_timeline_response.additional_properties = d
        return sidecar_timeline_response

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
