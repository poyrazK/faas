from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationExecution")


@_attrs_define
class OperationExecution:
    """Retained execution generation and ledger attempt count without private invocation data."""

    generation: int
    invocation_id: UUID
    state: str
    attempts: int
    created_at: datetime.datetime
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generation = self.generation

        invocation_id = str(self.invocation_id)

        state = self.state

        attempts = self.attempts

        created_at = self.created_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generation": generation,
                "invocation_id": invocation_id,
                "state": state,
                "attempts": attempts,
                "created_at": created_at,
            }
        )
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        generation = d.pop("generation")

        invocation_id = UUID(d.pop("invocation_id"))

        state = d.pop("state")

        attempts = d.pop("attempts")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        operation_execution = cls(
            generation=generation,
            invocation_id=invocation_id,
            state=state,
            attempts=attempts,
            created_at=created_at,
            completed_at=completed_at,
        )

        operation_execution.additional_properties = d
        return operation_execution

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
