from typing import Literal

SetPlatformTenantCredentialPolicyRequestAllowedScopesItem = Literal["admin", "read", "write"]

SET_PLATFORM_TENANT_CREDENTIAL_POLICY_REQUEST_ALLOWED_SCOPES_ITEM_VALUES: set[
    SetPlatformTenantCredentialPolicyRequestAllowedScopesItem
] = {
    "admin",
    "read",
    "write",
}


def check_set_platform_tenant_credential_policy_request_allowed_scopes_item(
    value: str,
) -> SetPlatformTenantCredentialPolicyRequestAllowedScopesItem:
    if value in SET_PLATFORM_TENANT_CREDENTIAL_POLICY_REQUEST_ALLOWED_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_PLATFORM_TENANT_CREDENTIAL_POLICY_REQUEST_ALLOWED_SCOPES_ITEM_VALUES!r}"
    )
