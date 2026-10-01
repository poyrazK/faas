from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.progressive_rollout import ProgressiveRollout


T = TypeVar("T", bound="FlagRule")


@_attrs_define
class FlagRule:
    """Ordered customer targeting rule; supplied constraints combine with AND. Boolean flags require a boolean value;
    variant flags may omit value to use weighted assignment. Progressive rollout is limited to boolean true rules.

    """

    id: str
    customers: list[UUID] | Unset = UNSET
    group: str | Unset = UNSET
    """Owner-managed customer group key."""
    rollout: int | Unset = UNSET
    """Basis points of eligible customers; omitted means all eligible customers."""
    progression: ProgressiveRollout | Unset = UNSET
    """Health-gated, operator-promoted stages for a boolean true rule. The rule rollout must equal
    stages[current_stage]; stages must strictly increase and end at 10000 basis points."""
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

        rollout = self.rollout

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
        if rollout is not UNSET:
            field_dict["rollout"] = rollout
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

        rollout = d.pop("rollout", UNSET)

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
            rollout=rollout,
            progression=progression,
            value=value,
        )

        return flag_rule
