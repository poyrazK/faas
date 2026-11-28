from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="InsightEventPoint")


@_attrs_define
class InsightEventPoint:
    """One time bucket and breakdown value of an event series."""

    start: datetime.datetime
    events: int
    units: int
    """Distinct customers or subjects; for (other) a sum that may count a unit more than once."""
    value: int
    breakdown: str | Unset = UNSET
    """Breakdown value; (none) without the flag or property, (other) past the breakdown bound."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        start = self.start.isoformat()

        events = self.events

        units = self.units

        value = self.value

        breakdown = self.breakdown

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "start": start,
                "events": events,
                "units": units,
                "value": value,
            }
        )
        if breakdown is not UNSET:
            field_dict["breakdown"] = breakdown

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        start = datetime.datetime.fromisoformat(d.pop("start"))

        events = d.pop("events")

        units = d.pop("units")

        value = d.pop("value")

        breakdown = d.pop("breakdown", UNSET)

        insight_event_point = cls(
            start=start,
            events=events,
            units=units,
            value=value,
            breakdown=breakdown,
        )

        insight_event_point.additional_properties = d
        return insight_event_point

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
