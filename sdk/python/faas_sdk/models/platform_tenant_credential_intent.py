from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_credential_intent_scopes_item import (
    PlatformTenantCredentialIntentScopesItem,
    check_platform_tenant_credential_intent_scopes_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantCredentialIntent")


@_attrs_define
class PlatformTenantCredentialIntent:
    """Metadata and SHA-256 digest of a locally generated ck_ credential; never submit plaintext."""

    consumer_id: UUID
    name: str
    prefix: str
    hash_: str
    """Hex-encoded SHA-256 of the entire plaintext key."""
    scopes: list[PlatformTenantCredentialIntentScopesItem]
    expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_id = str(self.consumer_id)

        name = self.name

        prefix = self.prefix

        hash_ = self.hash_

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
                "consumer_id": consumer_id,
                "name": name,
                "prefix": prefix,
                "hash": hash_,
                "scopes": scopes,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        consumer_id = UUID(d.pop("consumer_id"))

        name = d.pop("name")

        prefix = d.pop("prefix")

        hash_ = d.pop("hash")

        scopes = []
        _scopes = d.pop("scopes")
        for scopes_item_data in _scopes:
            scopes_item = check_platform_tenant_credential_intent_scopes_item(scopes_item_data)

            scopes.append(scopes_item)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        platform_tenant_credential_intent = cls(
            consumer_id=consumer_id,
            name=name,
            prefix=prefix,
            hash_=hash_,
            scopes=scopes,
            expires_at=expires_at,
        )

        platform_tenant_credential_intent.additional_properties = d
        return platform_tenant_credential_intent

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
