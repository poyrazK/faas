from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimePrincipalReceiptDelivery")


@_attrs_define
class ManagedRealtimePrincipalReceiptDelivery:
    """Per-connection receipt state for a principal message dispatch."""

    connection_id: str
    status: str
    queue_status: str
    ack_supported: bool
    created_at: str
    queued_at: str | Unset = UNSET
    acknowledged_at: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        connection_id = self.connection_id

        status = self.status

        queue_status = self.queue_status

        ack_supported = self.ack_supported

        created_at = self.created_at

        queued_at = self.queued_at

        acknowledged_at = self.acknowledged_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "connection_id": connection_id,
                "status": status,
                "queue_status": queue_status,
                "ack_supported": ack_supported,
                "created_at": created_at,
            }
        )
        if queued_at is not UNSET:
            field_dict["queued_at"] = queued_at
        if acknowledged_at is not UNSET:
            field_dict["acknowledged_at"] = acknowledged_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        connection_id = d.pop("connection_id")

        status = d.pop("status")

        queue_status = d.pop("queue_status")

        ack_supported = d.pop("ack_supported")

        created_at = d.pop("created_at")

        queued_at = d.pop("queued_at", UNSET)

        acknowledged_at = d.pop("acknowledged_at", UNSET)

        managed_realtime_principal_receipt_delivery = cls(
            connection_id=connection_id,
            status=status,
            queue_status=queue_status,
            ack_supported=ack_supported,
            created_at=created_at,
            queued_at=queued_at,
            acknowledged_at=acknowledged_at,
        )

        managed_realtime_principal_receipt_delivery.additional_properties = d
        return managed_realtime_principal_receipt_delivery

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
