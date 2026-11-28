from typing import Literal

ProjectReleaseStabilizationHealth = Literal["healthy", "regressed", "unknown"]

PROJECT_RELEASE_STABILIZATION_HEALTH_VALUES: set[ProjectReleaseStabilizationHealth] = {
    "healthy",
    "regressed",
    "unknown",
}


def check_project_release_stabilization_health(value: str) -> ProjectReleaseStabilizationHealth:
    if value in PROJECT_RELEASE_STABILIZATION_HEALTH_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_RELEASE_STABILIZATION_HEALTH_VALUES!r}")
