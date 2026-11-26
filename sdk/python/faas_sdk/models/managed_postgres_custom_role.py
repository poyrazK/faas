from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_custom_role_default_for_item import (
    ManagedPostgresCustomRoleDefaultForItem,
    check_managed_postgres_custom_role_default_for_item,
)
from ..models.managed_postgres_custom_role_granted_to_item import (
    ManagedPostgresCustomRoleGrantedToItem,
    check_managed_postgres_custom_role_granted_to_item,
)
from ..models.managed_postgres_custom_role_state import (
    ManagedPostgresCustomRoleState,
    check_managed_postgres_custom_role_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresCustomRole")


@_attrs_define
class ManagedPostgresCustomRole:
    """A custom group role declared on a managed database (ADR-20261116213000)."""

    database_id: str
    name: str
    bypass_rls: bool
    granted_to: list[ManagedPostgresCustomRoleGrantedToItem]
    """Access modes whose logins may SET ROLE to it (migration, read_write, read_only); read_write includes session
    logins."""
    member_of: list[str]
    """Other custom roles of the database it is a member of."""
    default_for: list[ManagedPostgresCustomRoleDefaultForItem]
    """Access modes whose logins start every session as this role."""
    state: ManagedPostgresCustomRoleState
    """pending until applied to the database, ready after, deleting until a deleted role is dropped."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    last_error_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = self.database_id

        name = self.name

        bypass_rls = self.bypass_rls

        granted_to = []
        for granted_to_item_data in self.granted_to:
            granted_to_item: str = granted_to_item_data
            granted_to.append(granted_to_item)

        member_of = self.member_of

        default_for = []
        for default_for_item_data in self.default_for:
            default_for_item: str = default_for_item_data
            default_for.append(default_for_item)

        state: str = self.state

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        last_error_code = self.last_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "name": name,
                "bypass_rls": bypass_rls,
                "granted_to": granted_to,
                "member_of": member_of,
                "default_for": default_for,
                "state": state,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = d.pop("database_id")

        name = d.pop("name")

        bypass_rls = d.pop("bypass_rls")

        granted_to = []
        _granted_to = d.pop("granted_to")
        for granted_to_item_data in _granted_to:
            granted_to_item = check_managed_postgres_custom_role_granted_to_item(granted_to_item_data)

            granted_to.append(granted_to_item)

        member_of = cast(list[str], d.pop("member_of"))

        default_for = []
        _default_for = d.pop("default_for")
        for default_for_item_data in _default_for:
            default_for_item = check_managed_postgres_custom_role_default_for_item(default_for_item_data)

            default_for.append(default_for_item)

        state = check_managed_postgres_custom_role_state(d.pop("state"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        last_error_code = d.pop("last_error_code", UNSET)

        managed_postgres_custom_role = cls(
            database_id=database_id,
            name=name,
            bypass_rls=bypass_rls,
            granted_to=granted_to,
            member_of=member_of,
            default_for=default_for,
            state=state,
            created_at=created_at,
            updated_at=updated_at,
            last_error_code=last_error_code,
        )

        managed_postgres_custom_role.additional_properties = d
        return managed_postgres_custom_role

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
