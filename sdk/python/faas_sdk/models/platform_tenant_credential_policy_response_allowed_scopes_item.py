from typing import Literal

PlatformTenantCredentialPolicyResponseAllowedScopesItem = Literal["admin", "read", "write"]

PLATFORM_TENANT_CREDENTIAL_POLICY_RESPONSE_ALLOWED_SCOPES_ITEM_VALUES: set[
    PlatformTenantCredentialPolicyResponseAllowedScopesItem
] = {
    "admin",
    "read",
    "write",
}


def check_platform_tenant_credential_policy_response_allowed_scopes_item(
    value: str,
) -> PlatformTenantCredentialPolicyResponseAllowedScopesItem:
    if value in PLATFORM_TENANT_CREDENTIAL_POLICY_RESPONSE_ALLOWED_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_CREDENTIAL_POLICY_RESPONSE_ALLOWED_SCOPES_ITEM_VALUES!r}"
    )
