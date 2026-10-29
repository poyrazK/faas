from typing import Literal

ProjectReleaseStabilizationStatus = Literal[
    "completed", "conflicted", "monitoring", "needs_operator", "rolled_back", "waiting"
]

PROJECT_RELEASE_STABILIZATION_STATUS_VALUES: set[ProjectReleaseStabilizationStatus] = {
    "completed",
    "conflicted",
    "monitoring",
    "needs_operator",
    "rolled_back",
    "waiting",
}


def check_project_release_stabilization_status(value: str) -> ProjectReleaseStabilizationStatus:
    if value in PROJECT_RELEASE_STABILIZATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_RELEASE_STABILIZATION_STATUS_VALUES!r}")
