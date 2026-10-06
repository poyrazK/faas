from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OperationProgress")


@_attrs_define
class OperationProgress:
    """Current bounded progress from the active execution attempt."""

    stage: str
    completed: int
    total: int
    attempt: int
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        stage = self.stage

        completed = self.completed

        total = self.total

        attempt = self.attempt

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "stage": stage,
                "completed": completed,
                "total": total,
                "attempt": attempt,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        stage = d.pop("stage")

        completed = d.pop("completed")

        total = d.pop("total")

        attempt = d.pop("attempt")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        operation_progress = cls(
            stage=stage,
            completed=completed,
            total=total,
            attempt=attempt,
            updated_at=updated_at,
        )

        operation_progress.additional_properties = d
        return operation_progress

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
