from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.realtime_dead_letter_replay_selection import RealtimeDeadLetterReplaySelection


T = TypeVar("T", bound="RealtimeDeadLetterBulkReplayRequest")


@_attrs_define
class RealtimeDeadLetterBulkReplayRequest:
    """Realtime Dead Letter Bulk Replay Request."""

    group_id: UUID
    items: list[RealtimeDeadLetterReplaySelection]
    """Unique sequences selected explicitly from a preview."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        group_id = str(self.group_id)

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "group_id": group_id,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_dead_letter_replay_selection import RealtimeDeadLetterReplaySelection

        d = dict(src_dict)
        group_id = UUID(d.pop("group_id"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = RealtimeDeadLetterReplaySelection.from_dict(items_item_data)

            items.append(items_item)

        realtime_dead_letter_bulk_replay_request = cls(
            group_id=group_id,
            items=items,
        )

        realtime_dead_letter_bulk_replay_request.additional_properties = d
        return realtime_dead_letter_bulk_replay_request

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
