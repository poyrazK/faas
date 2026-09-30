from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagRule")


@_attrs_define
class FlagRule:
    """Ordered customer targeting rule; supplied constraints combine with AND."""

    id: str
    value: bool
    customers: list[UUID] | Unset = UNSET
    group: str | Unset = UNSET
    """Owner-managed customer group key."""
    rollout: int | Unset = UNSET
    """Basis points of eligible customers; omitted means all eligible customers."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        value = self.value

        customers: list[str] | Unset = UNSET
        if not isinstance(self.customers, Unset):
            customers = []
            for customers_item_data in self.customers:
                customers_item = str(customers_item_data)
                customers.append(customers_item)

        group = self.group

        rollout = self.rollout

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "value": value,
            }
        )
        if customers is not UNSET:
            field_dict["customers"] = customers
        if group is not UNSET:
            field_dict["group"] = group
        if rollout is not UNSET:
            field_dict["rollout"] = rollout

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        value = d.pop("value")

        _customers = d.pop("customers", UNSET)
        customers: list[UUID] | Unset = UNSET
        if _customers is not UNSET:
            customers = []
            for customers_item_data in _customers:
                customers_item = UUID(customers_item_data)

                customers.append(customers_item)

        group = d.pop("group", UNSET)

        rollout = d.pop("rollout", UNSET)

        flag_rule = cls(
            id=id,
            value=value,
            customers=customers,
            group=group,
            rollout=rollout,
        )

        return flag_rule
