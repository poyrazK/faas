from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_flag_rule_plans_item import PlatformFlagRulePlansItem, check_platform_flag_rule_plans_item
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.platform_flag_ramp import PlatformFlagRamp


T = TypeVar("T", bound="PlatformFlagRule")


@_attrs_define
class PlatformFlagRule:
    """One targeting rule of a platform flag. Accounts, orgs, group, plans and rollout combine with AND; the first matching
    rule decides. Orgs match the organization a request acts under (ADR 20261021080640).

    """

    id: str
    value: bool
    accounts: list[UUID] | Unset = UNSET
    orgs: list[UUID] | Unset = UNSET
    group: str | Unset = UNSET
    plans: list[PlatformFlagRulePlansItem] | Unset = UNSET
    rollout: int | Unset = UNSET
    """Basis points of the accounts the other constraints admit."""
    starts_at: datetime.datetime | Unset = UNSET
    """The rule applies from this moment (inclusive); stored in UTC."""
    ends_at: datetime.datetime | Unset = UNSET
    """The rule stops applying at this moment (exclusive); must be after starts_at."""
    ramp: PlatformFlagRamp | Unset = UNSET
    """Advances the rule's rollout stage by stage (ADR 20261021080640). The rollout is the current stage; apid
    moves it on once the flag's version has held for interval_seconds and its health routes stayed under
    max_5xx_basis_points over that window with at least min_requests requests. On a breach it pauses (and for
    rollback returns one stage); a paused ramp waits for an operator to clear paused."""
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

        orgs: list[str] | Unset = UNSET
        if not isinstance(self.orgs, Unset):
            orgs = []
            for orgs_item_data in self.orgs:
                orgs_item = str(orgs_item_data)
                orgs.append(orgs_item)

        group = self.group

        plans: list[str] | Unset = UNSET
        if not isinstance(self.plans, Unset):
            plans = []
            for plans_item_data in self.plans:
                plans_item: str = plans_item_data
                plans.append(plans_item)

        rollout = self.rollout

        starts_at: str | Unset = UNSET
        if not isinstance(self.starts_at, Unset):
            starts_at = self.starts_at.isoformat()

        ends_at: str | Unset = UNSET
        if not isinstance(self.ends_at, Unset):
            ends_at = self.ends_at.isoformat()

        ramp: dict[str, Any] | Unset = UNSET
        if not isinstance(self.ramp, Unset):
            ramp = self.ramp.to_dict()

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
        if orgs is not UNSET:
            field_dict["orgs"] = orgs
        if group is not UNSET:
            field_dict["group"] = group
        if plans is not UNSET:
            field_dict["plans"] = plans
        if rollout is not UNSET:
            field_dict["rollout"] = rollout
        if starts_at is not UNSET:
            field_dict["starts_at"] = starts_at
        if ends_at is not UNSET:
            field_dict["ends_at"] = ends_at
        if ramp is not UNSET:
            field_dict["ramp"] = ramp

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_flag_ramp import PlatformFlagRamp

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

        _orgs = d.pop("orgs", UNSET)
        orgs: list[UUID] | Unset = UNSET
        if _orgs is not UNSET:
            orgs = []
            for orgs_item_data in _orgs:
                orgs_item = UUID(orgs_item_data)

                orgs.append(orgs_item)

        group = d.pop("group", UNSET)

        _plans = d.pop("plans", UNSET)
        plans: list[PlatformFlagRulePlansItem] | Unset = UNSET
        if _plans is not UNSET:
            plans = []
            for plans_item_data in _plans:
                plans_item = check_platform_flag_rule_plans_item(plans_item_data)

                plans.append(plans_item)

        rollout = d.pop("rollout", UNSET)

        _starts_at = d.pop("starts_at", UNSET)
        starts_at: datetime.datetime | Unset
        if isinstance(_starts_at, Unset):
            starts_at = UNSET
        else:
            starts_at = datetime.datetime.fromisoformat(_starts_at)

        _ends_at = d.pop("ends_at", UNSET)
        ends_at: datetime.datetime | Unset
        if isinstance(_ends_at, Unset):
            ends_at = UNSET
        else:
            ends_at = datetime.datetime.fromisoformat(_ends_at)

        _ramp = d.pop("ramp", UNSET)
        ramp: PlatformFlagRamp | Unset
        if isinstance(_ramp, Unset):
            ramp = UNSET
        else:
            ramp = PlatformFlagRamp.from_dict(_ramp)

        platform_flag_rule = cls(
            id=id,
            value=value,
            accounts=accounts,
            orgs=orgs,
            group=group,
            plans=plans,
            rollout=rollout,
            starts_at=starts_at,
            ends_at=ends_at,
            ramp=ramp,
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
