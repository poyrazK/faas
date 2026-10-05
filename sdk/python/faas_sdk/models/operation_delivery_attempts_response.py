from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_delivery_attempt import OperationDeliveryAttempt


T = TypeVar("T", bound="OperationDeliveryAttemptsResponse")


@_attrs_define
class OperationDeliveryAttemptsResponse:
    """Newest-first attempt page with an operation-bound continuation cursor."""

    operation_id: UUID
    delivery_id: UUID
    attempts: list[OperationDeliveryAttempt]
    next_cursor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        delivery_id = str(self.delivery_id)

        attempts = []
        for attempts_item_data in self.attempts:
            attempts_item = attempts_item_data.to_dict()
            attempts.append(attempts_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "delivery_id": delivery_id,
                "attempts": attempts,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_delivery_attempt import OperationDeliveryAttempt

        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        delivery_id = UUID(d.pop("delivery_id"))

        attempts = []
        _attempts = d.pop("attempts")
        for attempts_item_data in _attempts:
            attempts_item = OperationDeliveryAttempt.from_dict(attempts_item_data)

            attempts.append(attempts_item)

        next_cursor = d.pop("next_cursor", UNSET)

        operation_delivery_attempts_response = cls(
            operation_id=operation_id,
            delivery_id=delivery_id,
            attempts=attempts,
            next_cursor=next_cursor,
        )

        return operation_delivery_attempts_response
