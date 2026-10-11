from typing import Literal

ProfileDeploymentCheckStatus = Literal[
    "cancelled", "inconclusive", "no_regression_detected", "queued", "regressed", "running"
]

PROFILE_DEPLOYMENT_CHECK_STATUS_VALUES: set[ProfileDeploymentCheckStatus] = {
    "cancelled",
    "inconclusive",
    "no_regression_detected",
    "queued",
    "regressed",
    "running",
}


def check_profile_deployment_check_status(value: str) -> ProfileDeploymentCheckStatus:
    if value in PROFILE_DEPLOYMENT_CHECK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_DEPLOYMENT_CHECK_STATUS_VALUES!r}")
