from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_platform_tenant_access_token_response_scopes_item import (
    CreatePlatformTenantAccessTokenResponseScopesItem,
    check_create_platform_tenant_access_token_response_scopes_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreatePlatformTenantAccessTokenResponse")


@_attrs_define
class CreatePlatformTenantAccessTokenResponse:
    """Token metadata plus one-time plaintext bearer. Save the token securely; it is never retrievable again."""

    id: UUID
    tenant_id: UUID
    name: str
    prefix: str
    scopes: list[CreatePlatformTenantAccessTokenResponseScopesItem]
    created_at: datetime.datetime
    expires_at: datetime.datetime
    token: str
    """One-time plaintext bearer; emitted only by create."""
    last_used_at: datetime.datetime | Unset = UNSET
    revoked_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        tenant_id = str(self.tenant_id)

        name = self.name

        prefix = self.prefix

        scopes = []
        for scopes_item_data in self.scopes:
            scopes_item: str = scopes_item_data
            scopes.append(scopes_item)

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        token = self.token

        last_used_at: str | Unset = UNSET
        if not isinstance(self.last_used_at, Unset):
            last_used_at = self.last_used_at.isoformat()

        revoked_at: str | Unset = UNSET
        if not isinstance(self.revoked_at, Unset):
            revoked_at = self.revoked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "tenant_id": tenant_id,
                "name": name,
                "prefix": prefix,
                "scopes": scopes,
                "created_at": created_at,
                "expires_at": expires_at,
                "token": token,
            }
        )
        if last_used_at is not UNSET:
            field_dict["last_used_at"] = last_used_at
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        tenant_id = UUID(d.pop("tenant_id"))

        name = d.pop("name")

        prefix = d.pop("prefix")

        scopes = []
        _scopes = d.pop("scopes")
        for scopes_item_data in _scopes:
            scopes_item = check_create_platform_tenant_access_token_response_scopes_item(scopes_item_data)

            scopes.append(scopes_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        token = d.pop("token")

        _last_used_at = d.pop("last_used_at", UNSET)
        last_used_at: datetime.datetime | Unset
        if isinstance(_last_used_at, Unset):
            last_used_at = UNSET
        else:
            last_used_at = datetime.datetime.fromisoformat(_last_used_at)

        _revoked_at = d.pop("revoked_at", UNSET)
        revoked_at: datetime.datetime | Unset
        if isinstance(_revoked_at, Unset):
            revoked_at = UNSET
        else:
            revoked_at = datetime.datetime.fromisoformat(_revoked_at)

        create_platform_tenant_access_token_response = cls(
            id=id,
            tenant_id=tenant_id,
            name=name,
            prefix=prefix,
            scopes=scopes,
            created_at=created_at,
            expires_at=expires_at,
            token=token,
            last_used_at=last_used_at,
            revoked_at=revoked_at,
        )

        create_platform_tenant_access_token_response.additional_properties = d
        return create_platform_tenant_access_token_response

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
