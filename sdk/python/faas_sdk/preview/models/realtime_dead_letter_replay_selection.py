from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeDeadLetterReplaySelection")


@_attrs_define
class RealtimeDeadLetterReplaySelection:
    """Realtime Dead Letter Replay Selection."""

    sequence: str
    dead_since: datetime.datetime
    """Exact preview timestamp; preserve precision, do not round it."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        dead_since = self.dead_since.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "dead_since": dead_since,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        dead_since = datetime.datetime.fromisoformat(d.pop("dead_since"))

        realtime_dead_letter_replay_selection = cls(
            sequence=sequence,
            dead_since=dead_since,
        )

        realtime_dead_letter_replay_selection.additional_properties = d
        return realtime_dead_letter_replay_selection

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
