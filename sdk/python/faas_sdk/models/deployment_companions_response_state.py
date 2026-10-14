from typing import Literal

DeploymentCompanionsResponseState = Literal["available", "unavailable"]

DEPLOYMENT_COMPANIONS_RESPONSE_STATE_VALUES: set[DeploymentCompanionsResponseState] = {
    "available",
    "unavailable",
}


def check_deployment_companions_response_state(value: str) -> DeploymentCompanionsResponseState:
    if value in DEPLOYMENT_COMPANIONS_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_COMPANIONS_RESPONSE_STATE_VALUES!r}")
