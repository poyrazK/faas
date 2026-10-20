from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.flag_outcome import FlagOutcome


T = TypeVar("T", bound="FlagOutcomesResponse")


@_attrs_define
class FlagOutcomesResponse:
    """Operational outcomes grouped by evaluated flag type and value over one bounded window."""

    outcomes: list[FlagOutcome]
    truncated: bool
    """True when additional low-volume cohorts were omitted after retaining the 100 highest-volume groups."""
    window_start: datetime.datetime
    window_end: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        outcomes = []
        for outcomes_item_data in self.outcomes:
            outcomes_item = outcomes_item_data.to_dict()
            outcomes.append(outcomes_item)

        truncated = self.truncated

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "outcomes": outcomes,
                "truncated": truncated,
                "window_start": window_start,
                "window_end": window_end,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_outcome import FlagOutcome

        d = dict(src_dict)
        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = FlagOutcome.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        truncated = d.pop("truncated")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        flag_outcomes_response = cls(
            outcomes=outcomes,
            truncated=truncated,
            window_start=window_start,
            window_end=window_end,
        )

        flag_outcomes_response.additional_properties = d
        return flag_outcomes_response

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
