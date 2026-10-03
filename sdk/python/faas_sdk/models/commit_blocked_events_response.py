from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.commit_blocked_event_response import CommitBlockedEventResponse


T = TypeVar("T", bound="CommitBlockedEventsResponse")


@_attrs_define
class CommitBlockedEventsResponse:
    """Bounded snapshot of blocked source events, with the observation time and snapshot limit."""

    items: list[CommitBlockedEventResponse]
    limit: int
    observation: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        limit = self.limit

        observation = self.observation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "limit": limit,
                "observation": observation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.commit_blocked_event_response import CommitBlockedEventResponse

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = CommitBlockedEventResponse.from_dict(items_item_data)

            items.append(items_item)

        limit = d.pop("limit")

        observation = d.pop("observation")

        commit_blocked_events_response = cls(
            items=items,
            limit=limit,
            observation=observation,
        )

        commit_blocked_events_response.additional_properties = d
        return commit_blocked_events_response

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
