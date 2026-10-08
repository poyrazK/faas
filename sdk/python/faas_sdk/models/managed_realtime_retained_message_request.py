from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_retained_message_request_metadata import (
        ManagedRealtimeRetainedMessageRequestMetadata,
    )


T = TypeVar("T", bound="ManagedRealtimeRetainedMessageRequest")


@_attrs_define
class ManagedRealtimeRetainedMessageRequest:
    """Binary-safe payload to append to retained channel history."""

    data_base64: str
    """Standard base64 for at most 4096 decoded bytes."""
    expected_sequence: int | None | Unset = UNSET
    """Optional channel head sequence required before this retained publish; zero requires an empty channel."""
    metadata: ManagedRealtimeRetainedMessageRequestMetadata | Unset = UNSET
    """Routing metadata attached to this retained publish; at most 4096 encoded JSON bytes."""
    binary: bool | Unset = False
    idempotency_key: str | Unset = UNSET
    """Deduplicates identical writes while retained."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data_base64 = self.data_base64

        expected_sequence: int | None | Unset
        if isinstance(self.expected_sequence, Unset):
            expected_sequence = UNSET
        else:
            expected_sequence = self.expected_sequence

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        binary = self.binary

        idempotency_key = self.idempotency_key

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "data_base64": data_base64,
            }
        )
        if expected_sequence is not UNSET:
            field_dict["expected_sequence"] = expected_sequence
        if metadata is not UNSET:
            field_dict["metadata"] = metadata
        if binary is not UNSET:
            field_dict["binary"] = binary
        if idempotency_key is not UNSET:
            field_dict["idempotency_key"] = idempotency_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_retained_message_request_metadata import (
            ManagedRealtimeRetainedMessageRequestMetadata,
        )

        d = dict(src_dict)
        data_base64 = d.pop("data_base64")

        def _parse_expected_sequence(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        expected_sequence = _parse_expected_sequence(d.pop("expected_sequence", UNSET))

        _metadata = d.pop("metadata", UNSET)
        metadata: ManagedRealtimeRetainedMessageRequestMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = ManagedRealtimeRetainedMessageRequestMetadata.from_dict(_metadata)

        binary = d.pop("binary", UNSET)

        idempotency_key = d.pop("idempotency_key", UNSET)

        managed_realtime_retained_message_request = cls(
            data_base64=data_base64,
            expected_sequence=expected_sequence,
            metadata=metadata,
            binary=binary,
            idempotency_key=idempotency_key,
        )

        managed_realtime_retained_message_request.additional_properties = d
        return managed_realtime_retained_message_request

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
