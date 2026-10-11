from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_message_request_metadata import ManagedRealtimeMessageRequestMetadata


T = TypeVar("T", bound="ManagedRealtimeMessageRequest")


@_attrs_define
class ManagedRealtimeMessageRequest:
    """Binary-safe message payload encoded as standard base64."""

    data_base64: str
    """Decoded payload is limited to 1 MiB."""
    expected_sequence: int | None | Unset = UNSET
    """Optional current channel sequence precondition; zero requires an empty channel. Retained channel writes
    only."""
    metadata: ManagedRealtimeMessageRequestMetadata | Unset = UNSET
    """Exact-match routing metadata; at most 4096 encoded JSON bytes. Retained channel publishing only."""
    binary: bool | Unset = False
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_message_request_metadata import ManagedRealtimeMessageRequestMetadata

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
        metadata: ManagedRealtimeMessageRequestMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = ManagedRealtimeMessageRequestMetadata.from_dict(_metadata)

        binary = d.pop("binary", UNSET)

        managed_realtime_message_request = cls(
            data_base64=data_base64,
            expected_sequence=expected_sequence,
            metadata=metadata,
            binary=binary,
        )

        managed_realtime_message_request.additional_properties = d
        return managed_realtime_message_request

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
