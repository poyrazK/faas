from typing import Literal

CreateDeployTokenRequestScopesItem = Literal[
    "agent", "apps:read", "deploy:write", "env:write", "secrets:request", "storage:manage", "usage:read"
]

CREATE_DEPLOY_TOKEN_REQUEST_SCOPES_ITEM_VALUES: set[CreateDeployTokenRequestScopesItem] = {
    "agent",
    "apps:read",
    "deploy:write",
    "env:write",
    "secrets:request",
    "storage:manage",
    "usage:read",
}


def check_create_deploy_token_request_scopes_item(value: str) -> CreateDeployTokenRequestScopesItem:
    if value in CREATE_DEPLOY_TOKEN_REQUEST_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_DEPLOY_TOKEN_REQUEST_SCOPES_ITEM_VALUES!r}")
