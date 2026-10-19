from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.binding_verification_qualification_condition import BindingVerificationQualificationCondition


T = TypeVar("T", bound="BindingVerificationQualification")


@_attrs_define
class BindingVerificationQualification:
    """Server-confirmed temporary qualification conditions for the original receipt. No admission or release authority;
    wait within its original expiry and recheck.

    """

    checked_at: datetime.datetime
    conditions: list[BindingVerificationQualificationCondition]

    def to_dict(self) -> dict[str, Any]:
        checked_at = self.checked_at.isoformat()

        conditions = []
        for conditions_item_data in self.conditions:
            conditions_item = conditions_item_data.to_dict()
            conditions.append(conditions_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "checked_at": checked_at,
                "conditions": conditions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_verification_qualification_condition import BindingVerificationQualificationCondition

        d = dict(src_dict)
        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        conditions = []
        _conditions = d.pop("conditions")
        for conditions_item_data in _conditions:
            conditions_item = BindingVerificationQualificationCondition.from_dict(conditions_item_data)

            conditions.append(conditions_item)

        binding_verification_qualification = cls(
            checked_at=checked_at,
            conditions=conditions,
        )

        return binding_verification_qualification
