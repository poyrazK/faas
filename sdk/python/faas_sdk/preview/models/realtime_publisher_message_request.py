from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.realtime_publisher_message_request_metadata import RealtimePublisherMessageRequestMetadata


T = TypeVar("T", bound="RealtimePublisherMessageRequest")


@_attrs_define
class RealtimePublisherMessageRequest:
    """Realtime Publisher Message Request."""

    event_type: str
    data_base64: str
    ttl_seconds: int | Unset = 0
    """Message lifetime from retained publication; zero disables expiry. Positive credential caps forbid zero or
    omitted TTL."""
    binary: bool | Unset = False
    metadata: RealtimePublisherMessageRequestMetadata | Unset = UNSET
    """event_type is reserved; supplied value must match the top-level event_type."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_type = self.event_type

        data_base64 = self.data_base64

        ttl_seconds = self.ttl_seconds

        binary = self.binary

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_type": event_type,
                "data_base64": data_base64,
            }
        )
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds
        if binary is not UNSET:
            field_dict["binary"] = binary
        if metadata is not UNSET:
            field_dict["metadata"] = metadata

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_publisher_message_request_metadata import RealtimePublisherMessageRequestMetadata

        d = dict(src_dict)
        event_type = d.pop("event_type")

        data_base64 = d.pop("data_base64")

        ttl_seconds = d.pop("ttl_seconds", UNSET)

        binary = d.pop("binary", UNSET)

        _metadata = d.pop("metadata", UNSET)
        metadata: RealtimePublisherMessageRequestMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = RealtimePublisherMessageRequestMetadata.from_dict(_metadata)

        realtime_publisher_message_request = cls(
            event_type=event_type,
            data_base64=data_base64,
            ttl_seconds=ttl_seconds,
            binary=binary,
            metadata=metadata,
        )

        realtime_publisher_message_request.additional_properties = d
        return realtime_publisher_message_request

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
