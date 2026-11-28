from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.put_managed_postgres_custom_role_request_default_for_item import (
    PutManagedPostgresCustomRoleRequestDefaultForItem,
    check_put_managed_postgres_custom_role_request_default_for_item,
)
from ..models.put_managed_postgres_custom_role_request_granted_to_item import (
    PutManagedPostgresCustomRoleRequestGrantedToItem,
    check_put_managed_postgres_custom_role_request_granted_to_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PutManagedPostgresCustomRoleRequest")


@_attrs_define
class PutManagedPostgresCustomRoleRequest:
    """The declaration of a custom role; absent fields take their defaults."""

    bypass_rls: bool | Unset = False
    granted_to: list[PutManagedPostgresCustomRoleRequestGrantedToItem] | Unset = UNSET
    """ Access modes whose logins may SET ROLE to it; session is stored as read_write. """
    member_of: list[str] | Unset = UNSET
    default_for: list[PutManagedPostgresCustomRoleRequestDefaultForItem] | Unset = UNSET
    """ When present, replaces the access modes whose logins start as this role; absent keeps the current ones that
    are still granted. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        bypass_rls = self.bypass_rls

        granted_to: list[str] | Unset = UNSET
        if not isinstance(self.granted_to, Unset):
            granted_to = []
            for granted_to_item_data in self.granted_to:
                granted_to_item: str = granted_to_item_data
                granted_to.append(granted_to_item)

        member_of: list[str] | Unset = UNSET
        if not isinstance(self.member_of, Unset):
            member_of = self.member_of

        default_for: list[str] | Unset = UNSET
        if not isinstance(self.default_for, Unset):
            default_for = []
            for default_for_item_data in self.default_for:
                default_for_item: str = default_for_item_data
                default_for.append(default_for_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if bypass_rls is not UNSET:
            field_dict["bypass_rls"] = bypass_rls
        if granted_to is not UNSET:
            field_dict["granted_to"] = granted_to
        if member_of is not UNSET:
            field_dict["member_of"] = member_of
        if default_for is not UNSET:
            field_dict["default_for"] = default_for

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        bypass_rls = d.pop("bypass_rls", UNSET)

        _granted_to = d.pop("granted_to", UNSET)
        granted_to: list[PutManagedPostgresCustomRoleRequestGrantedToItem] | Unset = UNSET
        if _granted_to is not UNSET:
            granted_to = []
            for granted_to_item_data in _granted_to:
                granted_to_item = check_put_managed_postgres_custom_role_request_granted_to_item(granted_to_item_data)

                granted_to.append(granted_to_item)

        member_of = cast(list[str], d.pop("member_of", UNSET))

        _default_for = d.pop("default_for", UNSET)
        default_for: list[PutManagedPostgresCustomRoleRequestDefaultForItem] | Unset = UNSET
        if _default_for is not UNSET:
            default_for = []
            for default_for_item_data in _default_for:
                default_for_item = check_put_managed_postgres_custom_role_request_default_for_item(
                    default_for_item_data
                )

                default_for.append(default_for_item)

        put_managed_postgres_custom_role_request = cls(
            bypass_rls=bypass_rls,
            granted_to=granted_to,
            member_of=member_of,
            default_for=default_for,
        )

        put_managed_postgres_custom_role_request.additional_properties = d
        return put_managed_postgres_custom_role_request

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
