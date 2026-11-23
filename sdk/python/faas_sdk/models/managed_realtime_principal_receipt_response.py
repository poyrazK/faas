from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_realtime_principal_receipt_delivery import ManagedRealtimePrincipalReceiptDelivery


T = TypeVar("T", bound="ManagedRealtimePrincipalReceiptResponse")


@_attrs_define
class ManagedRealtimePrincipalReceiptResponse:
    """Aggregate principal receipt and per-connection delivery states."""

    endpoint_id: str
    message_id: str
    status: str
    dispatch_complete: bool
    created_at: str
    expires_at: str
    recipients: int
    queued: int
    acknowledged: int
    pending: int
    timed_out: int
    unsupported: int
    queue_full: int
    failed: int
    nodes_queried: int
    nodes_unavailable: int
    partial: bool
    deliveries: list[ManagedRealtimePrincipalReceiptDelivery]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        endpoint_id = self.endpoint_id

        message_id = self.message_id

        status = self.status

        dispatch_complete = self.dispatch_complete

        created_at = self.created_at

        expires_at = self.expires_at

        recipients = self.recipients

        queued = self.queued

        acknowledged = self.acknowledged

        pending = self.pending

        timed_out = self.timed_out

        unsupported = self.unsupported

        queue_full = self.queue_full

        failed = self.failed

        nodes_queried = self.nodes_queried

        nodes_unavailable = self.nodes_unavailable

        partial = self.partial

        deliveries = []
        for deliveries_item_data in self.deliveries:
            deliveries_item = deliveries_item_data.to_dict()
            deliveries.append(deliveries_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "endpoint_id": endpoint_id,
                "message_id": message_id,
                "status": status,
                "dispatch_complete": dispatch_complete,
                "created_at": created_at,
                "expires_at": expires_at,
                "recipients": recipients,
                "queued": queued,
                "acknowledged": acknowledged,
                "pending": pending,
                "timed_out": timed_out,
                "unsupported": unsupported,
                "queue_full": queue_full,
                "failed": failed,
                "nodes_queried": nodes_queried,
                "nodes_unavailable": nodes_unavailable,
                "partial": partial,
                "deliveries": deliveries,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_principal_receipt_delivery import ManagedRealtimePrincipalReceiptDelivery

        d = dict(src_dict)
        endpoint_id = d.pop("endpoint_id")

        message_id = d.pop("message_id")

        status = d.pop("status")

        dispatch_complete = d.pop("dispatch_complete")

        created_at = d.pop("created_at")

        expires_at = d.pop("expires_at")

        recipients = d.pop("recipients")

        queued = d.pop("queued")

        acknowledged = d.pop("acknowledged")

        pending = d.pop("pending")

        timed_out = d.pop("timed_out")

        unsupported = d.pop("unsupported")

        queue_full = d.pop("queue_full")

        failed = d.pop("failed")

        nodes_queried = d.pop("nodes_queried")

        nodes_unavailable = d.pop("nodes_unavailable")

        partial = d.pop("partial")

        deliveries = []
        _deliveries = d.pop("deliveries")
        for deliveries_item_data in _deliveries:
            deliveries_item = ManagedRealtimePrincipalReceiptDelivery.from_dict(deliveries_item_data)

            deliveries.append(deliveries_item)

        managed_realtime_principal_receipt_response = cls(
            endpoint_id=endpoint_id,
            message_id=message_id,
            status=status,
            dispatch_complete=dispatch_complete,
            created_at=created_at,
            expires_at=expires_at,
            recipients=recipients,
            queued=queued,
            acknowledged=acknowledged,
            pending=pending,
            timed_out=timed_out,
            unsupported=unsupported,
            queue_full=queue_full,
            failed=failed,
            nodes_queried=nodes_queried,
            nodes_unavailable=nodes_unavailable,
            partial=partial,
            deliveries=deliveries,
        )

        managed_realtime_principal_receipt_response.additional_properties = d
        return managed_realtime_principal_receipt_response

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
