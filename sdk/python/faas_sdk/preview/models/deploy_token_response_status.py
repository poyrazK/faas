from typing import Literal

DeployTokenResponseStatus = Literal["active", "grace", "revoked"]

DEPLOY_TOKEN_RESPONSE_STATUS_VALUES: set[DeployTokenResponseStatus] = {
    "active",
    "grace",
    "revoked",
}


def check_deploy_token_response_status(value: str) -> DeployTokenResponseStatus:
    if value in DEPLOY_TOKEN_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOY_TOKEN_RESPONSE_STATUS_VALUES!r}")
