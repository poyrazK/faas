from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.publish_managed_realtime_channel_batch_body_messages_item import (
        PublishManagedRealtimeChannelBatchBodyMessagesItem,
    )


T = TypeVar("T", bound="PublishManagedRealtimeChannelBatchBody")


@_attrs_define
class PublishManagedRealtimeChannelBatchBody:
    batch_id: str
    messages: list[PublishManagedRealtimeChannelBatchBodyMessagesItem]
    expected_sequence: int | None | Unset = UNSET
    """Precondition for the channel head before the whole batch."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        batch_id = self.batch_id

        messages = []
        for messages_item_data in self.messages:
            messages_item = messages_item_data.to_dict()
            messages.append(messages_item)

        expected_sequence: int | None | Unset
        if isinstance(self.expected_sequence, Unset):
            expected_sequence = UNSET
        else:
            expected_sequence = self.expected_sequence

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "batch_id": batch_id,
                "messages": messages,
            }
        )
        if expected_sequence is not UNSET:
            field_dict["expected_sequence"] = expected_sequence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.publish_managed_realtime_channel_batch_body_messages_item import (
            PublishManagedRealtimeChannelBatchBodyMessagesItem,
        )

        d = dict(src_dict)
        batch_id = d.pop("batch_id")

        messages = []
        _messages = d.pop("messages")
        for messages_item_data in _messages:
            messages_item = PublishManagedRealtimeChannelBatchBodyMessagesItem.from_dict(messages_item_data)

            messages.append(messages_item)

        def _parse_expected_sequence(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        expected_sequence = _parse_expected_sequence(d.pop("expected_sequence", UNSET))

        publish_managed_realtime_channel_batch_body = cls(
            batch_id=batch_id,
            messages=messages,
            expected_sequence=expected_sequence,
        )

        publish_managed_realtime_channel_batch_body.additional_properties = d
        return publish_managed_realtime_channel_batch_body

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
