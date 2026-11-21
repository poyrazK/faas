from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_event import FlagEvent
    from ..models.flag_exposure import FlagExposure


T = TypeVar("T", bound="FlagEventsRequest")


@_attrs_define
class FlagEventsRequest:
    """A batch of runtime conversion events."""

    events: list[FlagEvent] | Unset = UNSET
    """1 to 100 events, or none when the batch carries only exposures."""
    exposures: list[FlagExposure] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        events: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.events, Unset):
            events = []
            for events_item_data in self.events:
                events_item = events_item_data.to_dict()
                events.append(events_item)

        exposures: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.exposures, Unset):
            exposures = []
            for exposures_item_data in self.exposures:
                exposures_item = exposures_item_data.to_dict()
                exposures.append(exposures_item)

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if events is not UNSET:
            field_dict["events"] = events
        if exposures is not UNSET:
            field_dict["exposures"] = exposures

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_event import FlagEvent
        from ..models.flag_exposure import FlagExposure

        d = dict(src_dict)
        _events = d.pop("events", UNSET)
        events: list[FlagEvent] | Unset = UNSET
        if _events is not UNSET:
            events = []
            for events_item_data in _events:
                events_item = FlagEvent.from_dict(events_item_data)

                events.append(events_item)

        _exposures = d.pop("exposures", UNSET)
        exposures: list[FlagExposure] | Unset = UNSET
        if _exposures is not UNSET:
            exposures = []
            for exposures_item_data in _exposures:
                exposures_item = FlagExposure.from_dict(exposures_item_data)

                exposures.append(exposures_item)

        flag_events_request = cls(
            events=events,
            exposures=exposures,
        )

        return flag_events_request
