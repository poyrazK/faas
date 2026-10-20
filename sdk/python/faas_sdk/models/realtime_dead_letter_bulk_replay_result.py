from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.realtime_dead_letter_replay_outcome import RealtimeDeadLetterReplayOutcome


T = TypeVar("T", bound="RealtimeDeadLetterBulkReplayResult")


@_attrs_define
class RealtimeDeadLetterBulkReplayResult:
    """Realtime Dead Letter Bulk Replay Result."""

    group_id: UUID
    replayed: int
    outcomes: list[RealtimeDeadLetterReplayOutcome]
    """One outcome per selection, in request order."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        group_id = str(self.group_id)

        replayed = self.replayed

        outcomes = []
        for outcomes_item_data in self.outcomes:
            outcomes_item = outcomes_item_data.to_dict()
            outcomes.append(outcomes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "group_id": group_id,
                "replayed": replayed,
                "outcomes": outcomes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_dead_letter_replay_outcome import RealtimeDeadLetterReplayOutcome

        d = dict(src_dict)
        group_id = UUID(d.pop("group_id"))

        replayed = d.pop("replayed")

        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = RealtimeDeadLetterReplayOutcome.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        realtime_dead_letter_bulk_replay_result = cls(
            group_id=group_id,
            replayed=replayed,
            outcomes=outcomes,
        )

        realtime_dead_letter_bulk_replay_result.additional_properties = d
        return realtime_dead_letter_bulk_replay_result

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
