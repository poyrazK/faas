from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_effect_record_status import OperationEffectRecordStatus, check_operation_effect_record_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationEffectRecord")


@_attrs_define
class OperationEffectRecord:
    """Immutable effect identity and current delivery status from the signed webhook ledger."""

    id: UUID
    name: str
    generation: int
    status: OperationEffectRecordStatus
    """recorded is an opaque effect with no adapter; unavailable indicates deleted or pruned delivery history.
    Completion of the operation does not imply delivery succeeded."""
    attempt: int
    webhook_id: UUID | Unset = UNSET
    delivery_id: UUID | Unset = UNSET
    """Stable across webhook retries; equals the effect ID."""
    type_: str | Unset = UNSET
    last_error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        generation = self.generation

        status: str = self.status

        attempt = self.attempt

        webhook_id: str | Unset = UNSET
        if not isinstance(self.webhook_id, Unset):
            webhook_id = str(self.webhook_id)

        delivery_id: str | Unset = UNSET
        if not isinstance(self.delivery_id, Unset):
            delivery_id = str(self.delivery_id)

        type_ = self.type_

        last_error = self.last_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "generation": generation,
                "status": status,
                "attempt": attempt,
            }
        )
        if webhook_id is not UNSET:
            field_dict["webhook_id"] = webhook_id
        if delivery_id is not UNSET:
            field_dict["delivery_id"] = delivery_id
        if type_ is not UNSET:
            field_dict["type"] = type_
        if last_error is not UNSET:
            field_dict["last_error"] = last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        generation = d.pop("generation")

        status = check_operation_effect_record_status(d.pop("status"))

        attempt = d.pop("attempt")

        _webhook_id = d.pop("webhook_id", UNSET)
        webhook_id: UUID | Unset
        if isinstance(_webhook_id, Unset):
            webhook_id = UNSET
        else:
            webhook_id = UUID(_webhook_id)

        _delivery_id = d.pop("delivery_id", UNSET)
        delivery_id: UUID | Unset
        if isinstance(_delivery_id, Unset):
            delivery_id = UNSET
        else:
            delivery_id = UUID(_delivery_id)

        type_ = d.pop("type", UNSET)

        last_error = d.pop("last_error", UNSET)

        operation_effect_record = cls(
            id=id,
            name=name,
            generation=generation,
            status=status,
            attempt=attempt,
            webhook_id=webhook_id,
            delivery_id=delivery_id,
            type_=type_,
            last_error=last_error,
        )

        operation_effect_record.additional_properties = d
        return operation_effect_record

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
