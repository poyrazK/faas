from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_retained_message_response_metadata import (
        ManagedRealtimeRetainedMessageResponseMetadata,
    )


T = TypeVar("T", bound="ManagedRealtimeRetainedMessageResponse")


@_attrs_define
class ManagedRealtimeRetainedMessageResponse:
    """A committed message and its channel-scoped sequence."""

    sequence: int
    data_base64: str
    binary: bool
    created_at: datetime.datetime
    metadata: ManagedRealtimeRetainedMessageResponseMetadata | Unset = UNSET
    """Exact-match routing metadata persisted with this channel message."""
    target_message_id: str | Unset = UNSET
    version: int | Unset = UNSET
    event: str | Unset = UNSET
    deleted: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        data_base64 = self.data_base64

        binary = self.binary

        created_at = self.created_at.isoformat()

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        target_message_id = self.target_message_id

        version = self.version

        event = self.event

        deleted = self.deleted

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "data_base64": data_base64,
                "binary": binary,
                "created_at": created_at,
            }
        )
        if metadata is not UNSET:
            field_dict["metadata"] = metadata
        if target_message_id is not UNSET:
            field_dict["target_message_id"] = target_message_id
        if version is not UNSET:
            field_dict["version"] = version
        if event is not UNSET:
            field_dict["event"] = event
        if deleted is not UNSET:
            field_dict["deleted"] = deleted

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_retained_message_response_metadata import (
            ManagedRealtimeRetainedMessageResponseMetadata,
        )

        d = dict(src_dict)
        sequence = d.pop("sequence")

        data_base64 = d.pop("data_base64")

        binary = d.pop("binary")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _metadata = d.pop("metadata", UNSET)
        metadata: ManagedRealtimeRetainedMessageResponseMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = ManagedRealtimeRetainedMessageResponseMetadata.from_dict(_metadata)

        target_message_id = d.pop("target_message_id", UNSET)

        version = d.pop("version", UNSET)

        event = d.pop("event", UNSET)

        deleted = d.pop("deleted", UNSET)

        managed_realtime_retained_message_response = cls(
            sequence=sequence,
            data_base64=data_base64,
            binary=binary,
            created_at=created_at,
            metadata=metadata,
            target_message_id=target_message_id,
            version=version,
            event=event,
            deleted=deleted,
        )

        managed_realtime_retained_message_response.additional_properties = d
        return managed_realtime_retained_message_response

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
