from typing import Literal

CreatePlatformTenantAccessTokenResponseScopesItem = Literal[
    "platform_tenant:statements:read", "platform_tenant:usage:read"
]

CREATE_PLATFORM_TENANT_ACCESS_TOKEN_RESPONSE_SCOPES_ITEM_VALUES: set[
    CreatePlatformTenantAccessTokenResponseScopesItem
] = {
    "platform_tenant:statements:read",
    "platform_tenant:usage:read",
}


def check_create_platform_tenant_access_token_response_scopes_item(
    value: str,
) -> CreatePlatformTenantAccessTokenResponseScopesItem:
    if value in CREATE_PLATFORM_TENANT_ACCESS_TOKEN_RESPONSE_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_PLATFORM_TENANT_ACCESS_TOKEN_RESPONSE_SCOPES_ITEM_VALUES!r}"
    )
