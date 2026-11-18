from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="InsightRetentionRow")


@_attrs_define
class InsightRetentionRow:
    """A retention cohort, breakdown value or the overall row."""

    units: int
    eligible: list[int]
    """Units whose period k has ended (index k-1)."""
    returned: list[int]
    rates: list[float]
    start: datetime.datetime | Unset = UNSET
    """Cohort start; absent on the overall and breakdown rows."""
    value: str | Unset = UNSET
    """Breakdown value."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        units = self.units

        eligible = self.eligible

        returned = self.returned

        rates = self.rates

        start: str | Unset = UNSET
        if not isinstance(self.start, Unset):
            start = self.start.isoformat()

        value = self.value

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "units": units,
                "eligible": eligible,
                "returned": returned,
                "rates": rates,
            }
        )
        if start is not UNSET:
            field_dict["start"] = start
        if value is not UNSET:
            field_dict["value"] = value

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        units = d.pop("units")

        eligible = cast(list[int], d.pop("eligible"))

        returned = cast(list[int], d.pop("returned"))

        rates = cast(list[float], d.pop("rates"))

        _start = d.pop("start", UNSET)
        start: datetime.datetime | Unset
        if isinstance(_start, Unset):
            start = UNSET
        else:
            start = datetime.datetime.fromisoformat(_start)

        value = d.pop("value", UNSET)

        insight_retention_row = cls(
            units=units,
            eligible=eligible,
            returned=returned,
            rates=rates,
            start=start,
            value=value,
        )

        insight_retention_row.additional_properties = d
        return insight_retention_row

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
