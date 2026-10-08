from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimePrincipalSendResponse")


@_attrs_define
class ManagedRealtimePrincipalSendResponse:
    """Principal send outcome including fleet queueing and retained notification acceptance."""

    message_id: str | Unset = UNSET
    sequence: int | Unset = UNSET
    durable: bool | Unset = UNSET
    fallback_deadline: datetime.datetime | Unset = UNSET
    recipients: int | Unset = UNSET
    queued: int | Unset = UNSET
    unsupported: int | Unset = UNSET
    queue_full: int | Unset = UNSET
    failed: int | Unset = UNSET
    nodes_queried: int | Unset = UNSET
    nodes_unavailable: int | Unset = UNSET
    partial: bool | Unset = UNSET
    receipt_requested: bool | Unset = UNSET
    receipt_status: str | Unset = UNSET
    acknowledged: int | Unset = UNSET
    pending: int | Unset = UNSET
    timed_out: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        message_id = self.message_id

        sequence = self.sequence

        durable = self.durable

        fallback_deadline: str | Unset = UNSET
        if not isinstance(self.fallback_deadline, Unset):
            fallback_deadline = self.fallback_deadline.isoformat()

        recipients = self.recipients

        queued = self.queued

        unsupported = self.unsupported

        queue_full = self.queue_full

        failed = self.failed

        nodes_queried = self.nodes_queried

        nodes_unavailable = self.nodes_unavailable

        partial = self.partial

        receipt_requested = self.receipt_requested

        receipt_status = self.receipt_status

        acknowledged = self.acknowledged

        pending = self.pending

        timed_out = self.timed_out

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if message_id is not UNSET:
            field_dict["message_id"] = message_id
        if sequence is not UNSET:
            field_dict["sequence"] = sequence
        if durable is not UNSET:
            field_dict["durable"] = durable
        if fallback_deadline is not UNSET:
            field_dict["fallback_deadline"] = fallback_deadline
        if recipients is not UNSET:
            field_dict["recipients"] = recipients
        if queued is not UNSET:
            field_dict["queued"] = queued
        if unsupported is not UNSET:
            field_dict["unsupported"] = unsupported
        if queue_full is not UNSET:
            field_dict["queue_full"] = queue_full
        if failed is not UNSET:
            field_dict["failed"] = failed
        if nodes_queried is not UNSET:
            field_dict["nodes_queried"] = nodes_queried
        if nodes_unavailable is not UNSET:
            field_dict["nodes_unavailable"] = nodes_unavailable
        if partial is not UNSET:
            field_dict["partial"] = partial
        if receipt_requested is not UNSET:
            field_dict["receipt_requested"] = receipt_requested
        if receipt_status is not UNSET:
            field_dict["receipt_status"] = receipt_status
        if acknowledged is not UNSET:
            field_dict["acknowledged"] = acknowledged
        if pending is not UNSET:
            field_dict["pending"] = pending
        if timed_out is not UNSET:
            field_dict["timed_out"] = timed_out

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        message_id = d.pop("message_id", UNSET)

        sequence = d.pop("sequence", UNSET)

        durable = d.pop("durable", UNSET)

        _fallback_deadline = d.pop("fallback_deadline", UNSET)
        fallback_deadline: datetime.datetime | Unset
        if isinstance(_fallback_deadline, Unset):
            fallback_deadline = UNSET
        else:
            fallback_deadline = datetime.datetime.fromisoformat(_fallback_deadline)

        recipients = d.pop("recipients", UNSET)

        queued = d.pop("queued", UNSET)

        unsupported = d.pop("unsupported", UNSET)

        queue_full = d.pop("queue_full", UNSET)

        failed = d.pop("failed", UNSET)

        nodes_queried = d.pop("nodes_queried", UNSET)

        nodes_unavailable = d.pop("nodes_unavailable", UNSET)

        partial = d.pop("partial", UNSET)

        receipt_requested = d.pop("receipt_requested", UNSET)

        receipt_status = d.pop("receipt_status", UNSET)

        acknowledged = d.pop("acknowledged", UNSET)

        pending = d.pop("pending", UNSET)

        timed_out = d.pop("timed_out", UNSET)

        managed_realtime_principal_send_response = cls(
            message_id=message_id,
            sequence=sequence,
            durable=durable,
            fallback_deadline=fallback_deadline,
            recipients=recipients,
            queued=queued,
            unsupported=unsupported,
            queue_full=queue_full,
            failed=failed,
            nodes_queried=nodes_queried,
            nodes_unavailable=nodes_unavailable,
            partial=partial,
            receipt_requested=receipt_requested,
            receipt_status=receipt_status,
            acknowledged=acknowledged,
            pending=pending,
            timed_out=timed_out,
        )

        managed_realtime_principal_send_response.additional_properties = d
        return managed_realtime_principal_send_response

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
