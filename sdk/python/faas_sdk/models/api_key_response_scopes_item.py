from typing import Literal

APIKeyResponseScopesItem = Literal[
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

API_KEY_RESPONSE_SCOPES_ITEM_VALUES: set[APIKeyResponseScopesItem] = {
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


def check_api_key_response_scopes_item(value: str) -> APIKeyResponseScopesItem:
    if value in API_KEY_RESPONSE_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_KEY_RESPONSE_SCOPES_ITEM_VALUES!r}")
