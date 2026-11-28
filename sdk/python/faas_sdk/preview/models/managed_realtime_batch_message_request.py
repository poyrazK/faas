from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_batch_message_request_metadata import ManagedRealtimeBatchMessageRequestMetadata


T = TypeVar("T", bound="ManagedRealtimeBatchMessageRequest")


@_attrs_define
class ManagedRealtimeBatchMessageRequest:
    """One retained publication in a channel batch, with an optional override of the batch message TTL."""

    data_base64: str
    ttl_seconds: int | None | Unset = UNSET
    """Override the batch TTL; omitted or null inherits the default and zero disables expiry for this message."""
    metadata: ManagedRealtimeBatchMessageRequestMetadata | Unset = UNSET
    binary: bool | Unset = False
    expected_sequence: None | Unset = UNSET
    """Per-message preconditions are unsupported. Omit this field or set it to null; use the batch-level
    expected_sequence to require a channel head."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data_base64 = self.data_base64

        ttl_seconds: int | None | Unset
        if isinstance(self.ttl_seconds, Unset):
            ttl_seconds = UNSET
        else:
            ttl_seconds = self.ttl_seconds

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        binary = self.binary

        expected_sequence = self.expected_sequence

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "data_base64": data_base64,
            }
        )
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds
        if metadata is not UNSET:
            field_dict["metadata"] = metadata
        if binary is not UNSET:
            field_dict["binary"] = binary
        if expected_sequence is not UNSET:
            field_dict["expected_sequence"] = expected_sequence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_batch_message_request_metadata import ManagedRealtimeBatchMessageRequestMetadata

        d = dict(src_dict)
        data_base64 = d.pop("data_base64")

        def _parse_ttl_seconds(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ttl_seconds = _parse_ttl_seconds(d.pop("ttl_seconds", UNSET))

        _metadata = d.pop("metadata", UNSET)
        metadata: ManagedRealtimeBatchMessageRequestMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = ManagedRealtimeBatchMessageRequestMetadata.from_dict(_metadata)

        binary = d.pop("binary", UNSET)

        expected_sequence = d.pop("expected_sequence", UNSET)

        managed_realtime_batch_message_request = cls(
            data_base64=data_base64,
            ttl_seconds=ttl_seconds,
            metadata=metadata,
            binary=binary,
            expected_sequence=expected_sequence,
        )

        managed_realtime_batch_message_request.additional_properties = d
        return managed_realtime_batch_message_request

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
