from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.platform_tenant_credential_result import PlatformTenantCredentialResult


T = TypeVar("T", bound="ApplyPlatformTenantCredentialsResponse")


@_attrs_define
class ApplyPlatformTenantCredentialsResponse:
    """Atomic credential plan or applied result for one platform tenant."""

    tenant_id: UUID
    dry_run: bool
    keys: list[PlatformTenantCredentialResult]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        dry_run = self.dry_run

        keys = []
        for keys_item_data in self.keys:
            keys_item = keys_item_data.to_dict()
            keys.append(keys_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "dry_run": dry_run,
                "keys": keys,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_credential_result import PlatformTenantCredentialResult

        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        dry_run = d.pop("dry_run")

        keys = []
        _keys = d.pop("keys")
        for keys_item_data in _keys:
            keys_item = PlatformTenantCredentialResult.from_dict(keys_item_data)

            keys.append(keys_item)

        apply_platform_tenant_credentials_response = cls(
            tenant_id=tenant_id,
            dry_run=dry_run,
            keys=keys,
        )

        apply_platform_tenant_credentials_response.additional_properties = d
        return apply_platform_tenant_credentials_response

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
