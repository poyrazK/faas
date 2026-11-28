from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.queue_lease_message_headers import QueueLeaseMessageHeaders
    from ..models.queue_lease_message_payload import QueueLeaseMessagePayload


T = TypeVar("T", bound="QueueLeaseMessage")


@_attrs_define
class QueueLeaseMessage:
    """One leased message. Its receipt acts only while this lease is current."""

    id: str
    receipt: str
    """Opaque; acts only while this lease is current."""
    payload: QueueLeaseMessagePayload
    attempt: int
    enqueued_at: datetime.datetime
    lease_expires_at: datetime.datetime
    headers: QueueLeaseMessageHeaders | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        receipt = self.receipt

        payload = self.payload.to_dict()

        attempt = self.attempt

        enqueued_at = self.enqueued_at.isoformat()

        lease_expires_at = self.lease_expires_at.isoformat()

        headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.headers, Unset):
            headers = self.headers.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "receipt": receipt,
                "payload": payload,
                "attempt": attempt,
                "enqueued_at": enqueued_at,
                "lease_expires_at": lease_expires_at,
            }
        )
        if headers is not UNSET:
            field_dict["headers"] = headers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.queue_lease_message_headers import QueueLeaseMessageHeaders
        from ..models.queue_lease_message_payload import QueueLeaseMessagePayload

        d = dict(src_dict)
        id = d.pop("id")

        receipt = d.pop("receipt")

        payload = QueueLeaseMessagePayload.from_dict(d.pop("payload"))

        attempt = d.pop("attempt")

        enqueued_at = datetime.datetime.fromisoformat(d.pop("enqueued_at"))

        lease_expires_at = datetime.datetime.fromisoformat(d.pop("lease_expires_at"))

        _headers = d.pop("headers", UNSET)
        headers: QueueLeaseMessageHeaders | Unset
        if isinstance(_headers, Unset):
            headers = UNSET
        else:
            headers = QueueLeaseMessageHeaders.from_dict(_headers)

        queue_lease_message = cls(
            id=id,
            receipt=receipt,
            payload=payload,
            attempt=attempt,
            enqueued_at=enqueued_at,
            lease_expires_at=lease_expires_at,
            headers=headers,
        )

        queue_lease_message.additional_properties = d
        return queue_lease_message

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
