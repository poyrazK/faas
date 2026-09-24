from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.platform_tenant_credential_intent import PlatformTenantCredentialIntent


T = TypeVar("T", bound="ApplyPlatformTenantCredentialsRequest")


@_attrs_define
class ApplyPlatformTenantCredentialsRequest:
    """Additive key reconciliation and revocation in one transaction; provide at least one key or revocation."""

    dry_run: bool | Unset = False
    keys: list[PlatformTenantCredentialIntent] | Unset = UNSET
    revoke_key_ids: list[UUID] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        dry_run = self.dry_run

        keys: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.keys, Unset):
            keys = []
            for keys_item_data in self.keys:
                keys_item = keys_item_data.to_dict()
                keys.append(keys_item)

        revoke_key_ids: list[str] | Unset = UNSET
        if not isinstance(self.revoke_key_ids, Unset):
            revoke_key_ids = []
            for revoke_key_ids_item_data in self.revoke_key_ids:
                revoke_key_ids_item = str(revoke_key_ids_item_data)
                revoke_key_ids.append(revoke_key_ids_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if dry_run is not UNSET:
            field_dict["dry_run"] = dry_run
        if keys is not UNSET:
            field_dict["keys"] = keys
        if revoke_key_ids is not UNSET:
            field_dict["revoke_key_ids"] = revoke_key_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_credential_intent import PlatformTenantCredentialIntent

        d = dict(src_dict)
        dry_run = d.pop("dry_run", UNSET)

        _keys = d.pop("keys", UNSET)
        keys: list[PlatformTenantCredentialIntent] | Unset = UNSET
        if _keys is not UNSET:
            keys = []
            for keys_item_data in _keys:
                keys_item = PlatformTenantCredentialIntent.from_dict(keys_item_data)

                keys.append(keys_item)

        _revoke_key_ids = d.pop("revoke_key_ids", UNSET)
        revoke_key_ids: list[UUID] | Unset = UNSET
        if _revoke_key_ids is not UNSET:
            revoke_key_ids = []
            for revoke_key_ids_item_data in _revoke_key_ids:
                revoke_key_ids_item = UUID(revoke_key_ids_item_data)

                revoke_key_ids.append(revoke_key_ids_item)

        apply_platform_tenant_credentials_request = cls(
            dry_run=dry_run,
            keys=keys,
            revoke_key_ids=revoke_key_ids,
        )

        apply_platform_tenant_credentials_request.additional_properties = d
        return apply_platform_tenant_credentials_request

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
