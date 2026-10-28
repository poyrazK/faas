from typing import Literal

DeploymentResponseStatus = Literal[
    "building", "cancelled", "failed", "imaging", "live", "pending", "snapshotting", "superseded"
]

DEPLOYMENT_RESPONSE_STATUS_VALUES: set[DeploymentResponseStatus] = {
    "building",
    "cancelled",
    "failed",
    "imaging",
    "live",
    "pending",
    "snapshotting",
    "superseded",
}


def check_deployment_response_status(value: str) -> DeploymentResponseStatus:
    if value in DEPLOYMENT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RESPONSE_STATUS_VALUES!r}")
