from typing import Literal

ProjectDeploymentCheckStatus = Literal["blocked", "passed", "unknown"]

PROJECT_DEPLOYMENT_CHECK_STATUS_VALUES: set[ProjectDeploymentCheckStatus] = {
    "blocked",
    "passed",
    "unknown",
}


def check_project_deployment_check_status(value: str) -> ProjectDeploymentCheckStatus:
    if value in PROJECT_DEPLOYMENT_CHECK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_DEPLOYMENT_CHECK_STATUS_VALUES!r}")
