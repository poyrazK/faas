from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeInboxMessageResponse")


@_attrs_define
class ManagedRealtimeInboxMessageResponse:
    """Retained principal inbox event with payload and mutation metadata."""

    version: int
    event: str
    deleted: bool
    message_id: str
    sequence: int
    data_base64: str
    binary: bool
    created_at: str
    expires_at: str
    target_message_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        event = self.event

        deleted = self.deleted

        message_id = self.message_id

        sequence = self.sequence

        data_base64 = self.data_base64

        binary = self.binary

        created_at = self.created_at

        expires_at = self.expires_at

        target_message_id = self.target_message_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "event": event,
                "deleted": deleted,
                "message_id": message_id,
                "sequence": sequence,
                "data_base64": data_base64,
                "binary": binary,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if target_message_id is not UNSET:
            field_dict["target_message_id"] = target_message_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        event = d.pop("event")

        deleted = d.pop("deleted")

        message_id = d.pop("message_id")

        sequence = d.pop("sequence")

        data_base64 = d.pop("data_base64")

        binary = d.pop("binary")

        created_at = d.pop("created_at")

        expires_at = d.pop("expires_at")

        target_message_id = d.pop("target_message_id", UNSET)

        managed_realtime_inbox_message_response = cls(
            version=version,
            event=event,
            deleted=deleted,
            message_id=message_id,
            sequence=sequence,
            data_base64=data_base64,
            binary=binary,
            created_at=created_at,
            expires_at=expires_at,
            target_message_id=target_message_id,
        )

        managed_realtime_inbox_message_response.additional_properties = d
        return managed_realtime_inbox_message_response

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
