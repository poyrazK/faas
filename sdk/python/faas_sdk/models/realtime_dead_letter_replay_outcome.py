from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_dead_letter_replay_outcome_status import (
    RealtimeDeadLetterReplayOutcomeStatus,
    check_realtime_dead_letter_replay_outcome_status,
)

T = TypeVar("T", bound="RealtimeDeadLetterReplayOutcome")


@_attrs_define
class RealtimeDeadLetterReplayOutcome:
    """Realtime Dead Letter Replay Outcome."""

    sequence: str
    status: RealtimeDeadLetterReplayOutcomeStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        status = check_realtime_dead_letter_replay_outcome_status(d.pop("status"))

        realtime_dead_letter_replay_outcome = cls(
            sequence=sequence,
            status=status,
        )

        realtime_dead_letter_replay_outcome.additional_properties = d
        return realtime_dead_letter_replay_outcome

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
