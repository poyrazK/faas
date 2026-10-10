from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_channel_batch_request_messages_item_metadata import (
        ManagedRealtimeChannelBatchRequestMessagesItemMetadata,
    )


T = TypeVar("T", bound="ManagedRealtimeChannelBatchRequestMessagesItem")


@_attrs_define
class ManagedRealtimeChannelBatchRequestMessagesItem:
    data_base64: str
    metadata: ManagedRealtimeChannelBatchRequestMessagesItemMetadata | Unset = UNSET
    binary: bool | Unset = False
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data_base64 = self.data_base64

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        binary = self.binary

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "data_base64": data_base64,
            }
        )
        if metadata is not UNSET:
            field_dict["metadata"] = metadata
        if binary is not UNSET:
            field_dict["binary"] = binary

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_channel_batch_request_messages_item_metadata import (
            ManagedRealtimeChannelBatchRequestMessagesItemMetadata,
        )

        d = dict(src_dict)
        data_base64 = d.pop("data_base64")

        _metadata = d.pop("metadata", UNSET)
        metadata: ManagedRealtimeChannelBatchRequestMessagesItemMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = ManagedRealtimeChannelBatchRequestMessagesItemMetadata.from_dict(_metadata)

        binary = d.pop("binary", UNSET)

        managed_realtime_channel_batch_request_messages_item = cls(
            data_base64=data_base64,
            metadata=metadata,
            binary=binary,
        )

        managed_realtime_channel_batch_request_messages_item.additional_properties = d
        return managed_realtime_channel_batch_request_messages_item

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
