from typing import Literal

DeployTokenResponseScopesItem = Literal["deploy:write"]

DEPLOY_TOKEN_RESPONSE_SCOPES_ITEM_VALUES: set[DeployTokenResponseScopesItem] = {
    "deploy:write",
}


def check_deploy_token_response_scopes_item(value: str) -> DeployTokenResponseScopesItem:
    if value in DEPLOY_TOKEN_RESPONSE_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOY_TOKEN_RESPONSE_SCOPES_ITEM_VALUES!r}")
