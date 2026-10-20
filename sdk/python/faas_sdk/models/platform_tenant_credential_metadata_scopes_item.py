from typing import Literal

PlatformTenantCredentialMetadataScopesItem = Literal["admin", "read", "write"]

PLATFORM_TENANT_CREDENTIAL_METADATA_SCOPES_ITEM_VALUES: set[PlatformTenantCredentialMetadataScopesItem] = {
    "admin",
    "read",
    "write",
}


def check_platform_tenant_credential_metadata_scopes_item(value: str) -> PlatformTenantCredentialMetadataScopesItem:
    if value in PLATFORM_TENANT_CREDENTIAL_METADATA_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_CREDENTIAL_METADATA_SCOPES_ITEM_VALUES!r}"
    )
