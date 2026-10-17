from typing import Literal

ProjectSafeReleaseStatus = Literal[
    "activated",
    "building",
    "conflicted",
    "failed",
    "rollback_failed",
    "rolled_back",
    "rolling_back",
    "succeeded",
    "verifying",
]

PROJECT_SAFE_RELEASE_STATUS_VALUES: set[ProjectSafeReleaseStatus] = {
    "activated",
    "building",
    "conflicted",
    "failed",
    "rollback_failed",
    "rolled_back",
    "rolling_back",
    "succeeded",
    "verifying",
}


def check_project_safe_release_status(value: str) -> ProjectSafeReleaseStatus:
    if value in PROJECT_SAFE_RELEASE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_SAFE_RELEASE_STATUS_VALUES!r}")
