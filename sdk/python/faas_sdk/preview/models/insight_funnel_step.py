from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="InsightFunnelStep")


@_attrs_define
class InsightFunnelStep:
    """Units and conversion of one funnel step."""

    event: str
    units: int
    conversion_from_first: float
    conversion_from_previous: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event = self.event

        units = self.units

        conversion_from_first = self.conversion_from_first

        conversion_from_previous = self.conversion_from_previous

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event": event,
                "units": units,
                "conversion_from_first": conversion_from_first,
                "conversion_from_previous": conversion_from_previous,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event = d.pop("event")

        units = d.pop("units")

        conversion_from_first = d.pop("conversion_from_first")

        conversion_from_previous = d.pop("conversion_from_previous")

        insight_funnel_step = cls(
            event=event,
            units=units,
            conversion_from_first=conversion_from_first,
            conversion_from_previous=conversion_from_previous,
        )

        insight_funnel_step.additional_properties = d
        return insight_funnel_step

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
