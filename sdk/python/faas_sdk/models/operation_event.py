from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_event_type import OperationEventType, check_operation_event_type
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationEvent")


@_attrs_define
class OperationEvent:
    """Ordered retained event; execution identity may change under the same logical operation."""

    operation_id: UUID
    sequence: int
    type_: OperationEventType
    data: Any
    """Event-specific customer projection."""
    created_at: datetime.datetime
    execution_id: UUID | Unset = UNSET
    attempt: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        sequence = self.sequence

        type_: str = self.type_

        data = self.data

        created_at = self.created_at.isoformat()

        execution_id: str | Unset = UNSET
        if not isinstance(self.execution_id, Unset):
            execution_id = str(self.execution_id)

        attempt = self.attempt

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation_id": operation_id,
                "sequence": sequence,
                "type": type_,
                "data": data,
                "created_at": created_at,
            }
        )
        if execution_id is not UNSET:
            field_dict["execution_id"] = execution_id
        if attempt is not UNSET:
            field_dict["attempt"] = attempt

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        sequence = d.pop("sequence")

        type_ = check_operation_event_type(d.pop("type"))

        data = d.pop("data")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _execution_id = d.pop("execution_id", UNSET)
        execution_id: UUID | Unset
        if isinstance(_execution_id, Unset):
            execution_id = UNSET
        else:
            execution_id = UUID(_execution_id)

        attempt = d.pop("attempt", UNSET)

        operation_event = cls(
            operation_id=operation_id,
            sequence=sequence,
            type_=type_,
            data=data,
            created_at=created_at,
            execution_id=execution_id,
            attempt=attempt,
        )

        operation_event.additional_properties = d
        return operation_event

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
