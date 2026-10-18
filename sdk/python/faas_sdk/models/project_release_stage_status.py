from typing import Literal

ProjectReleaseStageStatus = Literal["blocked", "cancelled", "expired", "ready", "waiting"]

PROJECT_RELEASE_STAGE_STATUS_VALUES: set[ProjectReleaseStageStatus] = {
    "blocked",
    "cancelled",
    "expired",
    "ready",
    "waiting",
}


def check_project_release_stage_status(value: str) -> ProjectReleaseStageStatus:
    if value in PROJECT_RELEASE_STAGE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_RELEASE_STAGE_STATUS_VALUES!r}")
