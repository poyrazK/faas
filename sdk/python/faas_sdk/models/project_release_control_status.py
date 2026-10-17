from typing import Literal

ProjectReleaseControlStatus = Literal["blocked", "pending", "restored"]

PROJECT_RELEASE_CONTROL_STATUS_VALUES: set[ProjectReleaseControlStatus] = {
    "blocked",
    "pending",
    "restored",
}


def check_project_release_control_status(value: str) -> ProjectReleaseControlStatus:
    if value in PROJECT_RELEASE_CONTROL_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_RELEASE_CONTROL_STATUS_VALUES!r}")
