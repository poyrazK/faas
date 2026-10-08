from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_realtime_publish_response import ManagedRealtimePublishResponse


T = TypeVar("T", bound="PublishManagedRealtimeChannelBatchResponse200")


@_attrs_define
class PublishManagedRealtimeChannelBatchResponse200:
    batch_id: str
    durable: bool
    partial: bool
    messages: list[ManagedRealtimePublishResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        batch_id = self.batch_id

        durable = self.durable

        partial = self.partial

        messages = []
        for messages_item_data in self.messages:
            messages_item = messages_item_data.to_dict()
            messages.append(messages_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "batch_id": batch_id,
                "durable": durable,
                "partial": partial,
                "messages": messages,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_publish_response import ManagedRealtimePublishResponse

        d = dict(src_dict)
        batch_id = d.pop("batch_id")

        durable = d.pop("durable")

        partial = d.pop("partial")

        messages = []
        _messages = d.pop("messages")
        for messages_item_data in _messages:
            messages_item = ManagedRealtimePublishResponse.from_dict(messages_item_data)

            messages.append(messages_item)

        publish_managed_realtime_channel_batch_response_200 = cls(
            batch_id=batch_id,
            durable=durable,
            partial=partial,
            messages=messages,
        )

        publish_managed_realtime_channel_batch_response_200.additional_properties = d
        return publish_managed_realtime_channel_batch_response_200

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
