from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformFlagOptIns")


@_attrs_define
class PlatformFlagOptIns:
    """Stored value of platform_flag_opt_ins.<key>: the accounts and organizations that joined a self-serve preview."""

    accounts: list[UUID] | Unset = UNSET
    orgs: list[UUID] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
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

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if accounts is not UNSET:
            field_dict["accounts"] = accounts
        if orgs is not UNSET:
            field_dict["orgs"] = orgs

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
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

        platform_flag_opt_ins = cls(
            accounts=accounts,
            orgs=orgs,
        )

        platform_flag_opt_ins.additional_properties = d
        return platform_flag_opt_ins

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
