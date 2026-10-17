from typing import Literal

DeploymentReleaseStatus = Literal[
    "cancelled", "failed", "pending", "queued", "restoring", "running", "succeeded", "timed_out"
]

DEPLOYMENT_RELEASE_STATUS_VALUES: set[DeploymentReleaseStatus] = {
    "cancelled",
    "failed",
    "pending",
    "queued",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
}


def check_deployment_release_status(value: str) -> DeploymentReleaseStatus:
    if value in DEPLOYMENT_RELEASE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RELEASE_STATUS_VALUES!r}")
