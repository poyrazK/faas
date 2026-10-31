from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_wake_event import ObsWakeEvent


T = TypeVar("T", bound="ObsWakeTimeline")


@_attrs_define
class ObsWakeTimeline:
    """Every platform event recorded for one wake, oldest first."""

    wake_id: str
    events: list[ObsWakeEvent]
    truncated: bool
    """True when the wake recorded more than 1000 events."""
    subject: str | Unset = UNSET
    """The first subject the events name (the app)"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        wake_id = self.wake_id

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        truncated = self.truncated

        subject = self.subject

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "wake_id": wake_id,
                "events": events,
                "truncated": truncated,
            }
        )
        if subject is not UNSET:
            field_dict["subject"] = subject

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_wake_event import ObsWakeEvent

        d = dict(src_dict)
        wake_id = d.pop("wake_id")

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = ObsWakeEvent.from_dict(events_item_data)

            events.append(events_item)

        truncated = d.pop("truncated")

        subject = d.pop("subject", UNSET)

        obs_wake_timeline = cls(
            wake_id=wake_id,
            events=events,
            truncated=truncated,
            subject=subject,
        )

        obs_wake_timeline.additional_properties = d
        return obs_wake_timeline

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
