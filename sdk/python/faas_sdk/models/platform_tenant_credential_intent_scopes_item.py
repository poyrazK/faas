from typing import Literal

PlatformTenantCredentialIntentScopesItem = Literal["admin", "read", "write"]

PLATFORM_TENANT_CREDENTIAL_INTENT_SCOPES_ITEM_VALUES: set[PlatformTenantCredentialIntentScopesItem] = {
    "admin",
    "read",
    "write",
}


def check_platform_tenant_credential_intent_scopes_item(value: str) -> PlatformTenantCredentialIntentScopesItem:
    if value in PLATFORM_TENANT_CREDENTIAL_INTENT_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_CREDENTIAL_INTENT_SCOPES_ITEM_VALUES!r}"
    )
