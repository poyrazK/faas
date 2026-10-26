from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_platform_tenant_access_token_request_scopes_item import (
    CreatePlatformTenantAccessTokenRequestScopesItem,
    check_create_platform_tenant_access_token_request_scopes_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreatePlatformTenantAccessTokenRequest")


@_attrs_define
class CreatePlatformTenantAccessTokenRequest:
    """Mint a bearer for one downstream tenant. Plaintext is returned once and expires no later than one year after
    creation.

    """

    name: str
    scopes: list[CreatePlatformTenantAccessTokenRequestScopesItem]
    expires_at: datetime.datetime | Unset = UNSET
    """Defaults to 90 days from creation; maximum 365 days."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        scopes = []
        for scopes_item_data in self.scopes:
            scopes_item: str = scopes_item_data
            scopes.append(scopes_item)

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "scopes": scopes,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        scopes = []
        _scopes = d.pop("scopes")
        for scopes_item_data in _scopes:
            scopes_item = check_create_platform_tenant_access_token_request_scopes_item(scopes_item_data)

            scopes.append(scopes_item)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        create_platform_tenant_access_token_request = cls(
            name=name,
            scopes=scopes,
            expires_at=expires_at,
        )

        create_platform_tenant_access_token_request.additional_properties = d
        return create_platform_tenant_access_token_request

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
