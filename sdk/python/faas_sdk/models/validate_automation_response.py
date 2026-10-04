from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ValidateAutomationResponse")


@_attrs_define
class ValidateAutomationResponse:
    """Validation issues, execution order and nominal next schedule occurrence."""

    valid: bool
    issues: list[str]
    step_order: list[str]
    next_fire_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        valid = self.valid

        issues = self.issues

        step_order = self.step_order

        next_fire_at: str | Unset = UNSET
        if not isinstance(self.next_fire_at, Unset):
            next_fire_at = self.next_fire_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "valid": valid,
                "issues": issues,
                "step_order": step_order,
            }
        )
        if next_fire_at is not UNSET:
            field_dict["next_fire_at"] = next_fire_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        valid = d.pop("valid")

        issues = cast(list[str], d.pop("issues"))

        step_order = cast(list[str], d.pop("step_order"))

        _next_fire_at = d.pop("next_fire_at", UNSET)
        next_fire_at: datetime.datetime | Unset
        if isinstance(_next_fire_at, Unset):
            next_fire_at = UNSET
        else:
            next_fire_at = datetime.datetime.fromisoformat(_next_fire_at)

        validate_automation_response = cls(
            valid=valid,
            issues=issues,
            step_order=step_order,
            next_fire_at=next_fire_at,
        )

        return validate_automation_response
