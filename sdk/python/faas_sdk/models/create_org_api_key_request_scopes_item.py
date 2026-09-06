from typing import Literal

CreateOrgAPIKeyRequestScopesItem = Literal[
    "admin",
    "apps:read",
    "deploy:write",
    "env:read",
    "env:write",
    "postgres:manage",
    "postgres:read",
    "registry_credentials:read",
    "registry_credentials:write",
    "secrets:read",
    "secrets:write",
    "storage:manage",
    "storage:read",
    "storage:write",
    "upstreams:write",
    "usage:read",
]

CREATE_ORG_API_KEY_REQUEST_SCOPES_ITEM_VALUES: set[CreateOrgAPIKeyRequestScopesItem] = {
    "admin",
    "apps:read",
    "deploy:write",
    "env:read",
    "env:write",
    "postgres:manage",
    "postgres:read",
    "registry_credentials:read",
    "registry_credentials:write",
    "secrets:read",
    "secrets:write",
    "storage:manage",
    "storage:read",
    "storage:write",
    "upstreams:write",
    "usage:read",
}


def check_create_org_api_key_request_scopes_item(value: str) -> CreateOrgAPIKeyRequestScopesItem:
    if value in CREATE_ORG_API_KEY_REQUEST_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_ORG_API_KEY_REQUEST_SCOPES_ITEM_VALUES!r}")
