from __future__ import annotations

import datetime
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
    ttl_seconds: int | Unset = 0
    delivery_expires_at: datetime.datetime | Unset = UNSET
    expired: bool | Unset = False
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

        if not isinstance(self.ttl_seconds, Unset):
            field_dict["ttl_seconds"] = self.ttl_seconds
        if not isinstance(self.delivery_expires_at, Unset):
            field_dict["delivery_expires_at"] = self.delivery_expires_at.isoformat()
        if not isinstance(self.expired, Unset):
            field_dict["expired"] = self.expired
        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expired = d.pop("expired", UNSET)

        delivery_expires_at = d.pop("delivery_expires_at", UNSET)
        if not isinstance(delivery_expires_at, Unset):
            delivery_expires_at = datetime.datetime.fromisoformat(delivery_expires_at)

        ttl_seconds = d.pop("ttl_seconds", UNSET)

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
            expired=expired,
            delivery_expires_at=delivery_expires_at,
            ttl_seconds=ttl_seconds,
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
