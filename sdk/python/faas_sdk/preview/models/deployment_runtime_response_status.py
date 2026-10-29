from typing import Literal

DeploymentRuntimeResponseStatus = Literal["pinned", "unknown", "unsupported"]

DEPLOYMENT_RUNTIME_RESPONSE_STATUS_VALUES: set[DeploymentRuntimeResponseStatus] = {
    "pinned",
    "unknown",
    "unsupported",
}


def check_deployment_runtime_response_status(value: str) -> DeploymentRuntimeResponseStatus:
    if value in DEPLOYMENT_RUNTIME_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RUNTIME_RESPONSE_STATUS_VALUES!r}")
