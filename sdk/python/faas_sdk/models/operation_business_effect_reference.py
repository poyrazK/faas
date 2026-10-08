from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationBusinessEffectReference")


@_attrs_define
class OperationBusinessEffectReference:
    operation_id: UUID
    """Canonical nonzero UUID of the retained confirmed effect."""
    milestone_id: UUID
    """Canonical nonzero UUID of the retained confirmed effect."""

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        milestone_id = str(self.milestone_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "milestone_id": milestone_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        milestone_id = UUID(d.pop("milestone_id"))

        operation_business_effect_reference = cls(
            operation_id=operation_id,
            milestone_id=milestone_id,
        )

        return operation_business_effect_reference
