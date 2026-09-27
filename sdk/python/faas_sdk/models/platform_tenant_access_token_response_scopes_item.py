from typing import Literal

PlatformTenantAccessTokenResponseScopesItem = Literal["platform_tenant:statements:read", "platform_tenant:usage:read"]

PLATFORM_TENANT_ACCESS_TOKEN_RESPONSE_SCOPES_ITEM_VALUES: set[PlatformTenantAccessTokenResponseScopesItem] = {
    "platform_tenant:statements:read",
    "platform_tenant:usage:read",
}


def check_platform_tenant_access_token_response_scopes_item(value: str) -> PlatformTenantAccessTokenResponseScopesItem:
    if value in PLATFORM_TENANT_ACCESS_TOKEN_RESPONSE_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_ACCESS_TOKEN_RESPONSE_SCOPES_ITEM_VALUES!r}"
    )
