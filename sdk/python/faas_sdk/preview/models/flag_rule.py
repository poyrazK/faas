from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.flag_rule_rollout_unit import FlagRuleRolloutUnit, check_flag_rule_rollout_unit
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.progressive_rollout import ProgressiveRollout


T = TypeVar("T", bound="FlagRule")


@_attrs_define
class FlagRule:
    """Ordered targeting rule; customer/group and subject constraints combine with AND. Subject rules require a customer or
    group constraint. Subject IDs are opaque application IDs and are evaluated only after application authentication.
    Boolean flags require a boolean value; variant flags may omit value to use weighted assignment. Progressive rollout
    is limited to boolean true rules.

    """

    id: str
    customers: list[UUID] | Unset = UNSET
    group: str | Unset = UNSET
    """Owner-managed customer group key."""
    subjects: list[str] | Unset = UNSET
    """Opaque application subject IDs, matched inside the selected customers/group. Use stable internal IDs, not
    emails or display names."""
    rollout: int | Unset = UNSET
    """Basis points of eligible customers by default, or eligible subjects when rollout_unit is subject; omitted
    means all eligible targets."""
    rollout_unit: FlagRuleRolloutUnit | Unset = UNSET
    """Allocation unit for rollout percentages. Omitted preserves customer-level allocation. Subject rollout
    requires a customer or group constraint and an authenticated subject context."""
    progression: ProgressiveRollout | Unset = UNSET
    """Health-gated stages for a boolean true rule. Promotion is manual by default; auto_advance opts into server-
    managed promotion after a full healthy evidence window. The rule rollout must equal stages[current_stage];
    stages must strictly increase and end at 10000 basis points."""
    value: bool | str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        customers: list[str] | Unset = UNSET
        if not isinstance(self.customers, Unset):
            customers = []
            for customers_item_data in self.customers:
                customers_item = str(customers_item_data)
                customers.append(customers_item)

        group = self.group

        subjects: list[str] | Unset = UNSET
        if not isinstance(self.subjects, Unset):
            subjects = self.subjects

        rollout = self.rollout

        rollout_unit: str | Unset = UNSET
        if not isinstance(self.rollout_unit, Unset):
            rollout_unit = self.rollout_unit

        progression: dict[str, Any] | Unset = UNSET
        if not isinstance(self.progression, Unset):
            progression = self.progression.to_dict()

        value: bool | str | Unset
        if isinstance(self.value, Unset):
            value = UNSET
        else:
            value = self.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
            }
        )
        if customers is not UNSET:
            field_dict["customers"] = customers
        if group is not UNSET:
            field_dict["group"] = group
        if subjects is not UNSET:
            field_dict["subjects"] = subjects
        if rollout is not UNSET:
            field_dict["rollout"] = rollout
        if rollout_unit is not UNSET:
            field_dict["rollout_unit"] = rollout_unit
        if progression is not UNSET:
            field_dict["progression"] = progression
        if value is not UNSET:
            field_dict["value"] = value

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.progressive_rollout import ProgressiveRollout

        d = dict(src_dict)
        id = d.pop("id")

        _customers = d.pop("customers", UNSET)
        customers: list[UUID] | Unset = UNSET
        if _customers is not UNSET:
            customers = []
            for customers_item_data in _customers:
                customers_item = UUID(customers_item_data)

                customers.append(customers_item)

        group = d.pop("group", UNSET)

        subjects = cast(list[str], d.pop("subjects", UNSET))

        rollout = d.pop("rollout", UNSET)

        _rollout_unit = d.pop("rollout_unit", UNSET)
        rollout_unit: FlagRuleRolloutUnit | Unset
        if isinstance(_rollout_unit, Unset):
            rollout_unit = UNSET
        else:
            rollout_unit = check_flag_rule_rollout_unit(_rollout_unit)

        _progression = d.pop("progression", UNSET)
        progression: ProgressiveRollout | Unset
        if isinstance(_progression, Unset):
            progression = UNSET
        else:
            progression = ProgressiveRollout.from_dict(_progression)

        def _parse_value(data: object) -> bool | str | Unset:
            if isinstance(data, Unset):
                return data
            return cast(bool | str | Unset, data)

        value = _parse_value(d.pop("value", UNSET))

        flag_rule = cls(
            id=id,
            customers=customers,
            group=group,
            subjects=subjects,
            rollout=rollout,
            rollout_unit=rollout_unit,
            progression=progression,
            value=value,
        )

        return flag_rule
