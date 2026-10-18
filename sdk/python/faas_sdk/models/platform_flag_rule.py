from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_flag_rule_plans_item import PlatformFlagRulePlansItem, check_platform_flag_rule_plans_item
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformFlagRule")


@_attrs_define
class PlatformFlagRule:
    """One targeting rule of a platform flag. Accounts, group, plans and rollout combine with AND; the first matching rule
    decides.

    """

    id: str
    value: bool
    accounts: list[UUID] | Unset = UNSET
    group: str | Unset = UNSET
    plans: list[PlatformFlagRulePlansItem] | Unset = UNSET
    rollout: int | Unset = UNSET
    """Basis points of the accounts the other constraints admit."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        value = self.value

        accounts: list[str] | Unset = UNSET
        if not isinstance(self.accounts, Unset):
            accounts = []
            for accounts_item_data in self.accounts:
                accounts_item = str(accounts_item_data)
                accounts.append(accounts_item)

        group = self.group

        plans: list[str] | Unset = UNSET
        if not isinstance(self.plans, Unset):
            plans = []
            for plans_item_data in self.plans:
                plans_item: str = plans_item_data
                plans.append(plans_item)

        rollout = self.rollout

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "value": value,
            }
        )
        if accounts is not UNSET:
            field_dict["accounts"] = accounts
        if group is not UNSET:
            field_dict["group"] = group
        if plans is not UNSET:
            field_dict["plans"] = plans
        if rollout is not UNSET:
            field_dict["rollout"] = rollout

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        value = d.pop("value")

        _accounts = d.pop("accounts", UNSET)
        accounts: list[UUID] | Unset = UNSET
        if _accounts is not UNSET:
            accounts = []
            for accounts_item_data in _accounts:
                accounts_item = UUID(accounts_item_data)

                accounts.append(accounts_item)

        group = d.pop("group", UNSET)

        _plans = d.pop("plans", UNSET)
        plans: list[PlatformFlagRulePlansItem] | Unset = UNSET
        if _plans is not UNSET:
            plans = []
            for plans_item_data in _plans:
                plans_item = check_platform_flag_rule_plans_item(plans_item_data)

                plans.append(plans_item)

        rollout = d.pop("rollout", UNSET)

        platform_flag_rule = cls(
            id=id,
            value=value,
            accounts=accounts,
            group=group,
            plans=plans,
            rollout=rollout,
        )

        platform_flag_rule.additional_properties = d
        return platform_flag_rule

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
