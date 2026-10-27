from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_delivery_response_state import (
    OperationDeliveryResponseState,
    check_operation_delivery_response_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationDeliveryResponse")


@_attrs_define
class OperationDeliveryResponse:
    """Independent completion notification projection from the webhook outbox."""

    state: OperationDeliveryResponseState
    attempts: int
    delivery_id: UUID | Unset = UNSET
    last_error: str | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        attempts = self.attempts

        delivery_id: str | Unset = UNSET
        if not isinstance(self.delivery_id, Unset):
            delivery_id = str(self.delivery_id)

        last_error = self.last_error

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "attempts": attempts,
            }
        )
        if delivery_id is not UNSET:
            field_dict["delivery_id"] = delivery_id
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_operation_delivery_response_state(d.pop("state"))

        attempts = d.pop("attempts")

        _delivery_id = d.pop("delivery_id", UNSET)
        delivery_id: UUID | Unset
        if isinstance(_delivery_id, Unset):
            delivery_id = UNSET
        else:
            delivery_id = UUID(_delivery_id)

        last_error = d.pop("last_error", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        operation_delivery_response = cls(
            state=state,
            attempts=attempts,
            delivery_id=delivery_id,
            last_error=last_error,
            next_attempt_at=next_attempt_at,
        )

        operation_delivery_response.additional_properties = d
        return operation_delivery_response

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
