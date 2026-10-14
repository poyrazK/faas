from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.queue_lease_receipt_failure_reason import (
    QueueLeaseReceiptFailureReason,
    check_queue_lease_receipt_failure_reason,
)

T = TypeVar("T", bound="QueueLeaseReceiptFailure")


@_attrs_define
class QueueLeaseReceiptFailure:
    """A receipt that was not applied. lease_expired means it no longer owns its message."""

    receipt: str
    reason: QueueLeaseReceiptFailureReason
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        receipt = self.receipt

        reason: str = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "receipt": receipt,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        receipt = d.pop("receipt")

        reason = check_queue_lease_receipt_failure_reason(d.pop("reason"))

        queue_lease_receipt_failure = cls(
            receipt=receipt,
            reason=reason,
        )

        queue_lease_receipt_failure.additional_properties = d
        return queue_lease_receipt_failure

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
