from typing import Literal

CreatePlatformTenantAccessTokenRequestScopesItem = Literal[
    "platform_tenant:statements:read", "platform_tenant:usage:read"
]

CREATE_PLATFORM_TENANT_ACCESS_TOKEN_REQUEST_SCOPES_ITEM_VALUES: set[
    CreatePlatformTenantAccessTokenRequestScopesItem
] = {
    "platform_tenant:statements:read",
    "platform_tenant:usage:read",
}


def check_create_platform_tenant_access_token_request_scopes_item(
    value: str,
) -> CreatePlatformTenantAccessTokenRequestScopesItem:
    if value in CREATE_PLATFORM_TENANT_ACCESS_TOKEN_REQUEST_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_PLATFORM_TENANT_ACCESS_TOKEN_REQUEST_SCOPES_ITEM_VALUES!r}"
    )
