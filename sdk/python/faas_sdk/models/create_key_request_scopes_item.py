from typing import Literal

CreateKeyRequestScopesItem = Literal[
    "admin",
    "apps:read",
    "deploy:write",
    "env:read",
    "env:write",
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

CREATE_KEY_REQUEST_SCOPES_ITEM_VALUES: set[CreateKeyRequestScopesItem] = {
    "admin",
    "apps:read",
    "deploy:write",
    "env:read",
    "env:write",
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


def check_create_key_request_scopes_item(value: str) -> CreateKeyRequestScopesItem:
    if value in CREATE_KEY_REQUEST_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_KEY_REQUEST_SCOPES_ITEM_VALUES!r}")
