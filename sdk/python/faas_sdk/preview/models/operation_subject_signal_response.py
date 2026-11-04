from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_signal_receipt import OperationSignalReceipt


T = TypeVar("T", bound="OperationSubjectSignalResponse")


@_attrs_define
class OperationSubjectSignalResponse:
    """Receipts for the operations a subject signal reached."""

    delivered: list[OperationSignalReceipt]

    def to_dict(self) -> dict[str, Any]:
        delivered = []
        for delivered_item_data in self.delivered:
            delivered_item = delivered_item_data.to_dict()
            delivered.append(delivered_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "delivered": delivered,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_signal_receipt import OperationSignalReceipt

        d = dict(src_dict)
        delivered = []
        _delivered = d.pop("delivered")
        for delivered_item_data in _delivered:
            delivered_item = OperationSignalReceipt.from_dict(delivered_item_data)

            delivered.append(delivered_item)

        operation_subject_signal_response = cls(
            delivered=delivered,
        )

        return operation_subject_signal_response
