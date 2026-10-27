from typing import Literal

ProjectDeploymentReadinessStatus = Literal["blocked", "ready", "unknown"]

PROJECT_DEPLOYMENT_READINESS_STATUS_VALUES: set[ProjectDeploymentReadinessStatus] = {
    "blocked",
    "ready",
    "unknown",
}


def check_project_deployment_readiness_status(value: str) -> ProjectDeploymentReadinessStatus:
    if value in PROJECT_DEPLOYMENT_READINESS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_DEPLOYMENT_READINESS_STATUS_VALUES!r}")
