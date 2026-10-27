from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationMilestoneValidationResponse")


@_attrs_define
class OperationMilestoneValidationResponse:
    """Confirmation that all candidate facts passed validation under the current execution claim."""

    valid: bool
    """True when the batch satisfies the current execution fence and immutable schemas."""

    def to_dict(self) -> dict[str, Any]:
        valid = self.valid

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "valid": valid,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        valid = d.pop("valid")

        operation_milestone_validation_response = cls(
            valid=valid,
        )

        return operation_milestone_validation_response
