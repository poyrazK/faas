from typing import Literal

ProjectReleaseCommandStatus = Literal[
    "cancelled", "failed", "queued", "restoring", "running", "succeeded", "timed_out", "uncertain", "waiting"
]

PROJECT_RELEASE_COMMAND_STATUS_VALUES: set[ProjectReleaseCommandStatus] = {
    "cancelled",
    "failed",
    "queued",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
    "uncertain",
    "waiting",
}


def check_project_release_command_status(value: str) -> ProjectReleaseCommandStatus:
    if value in PROJECT_RELEASE_COMMAND_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_RELEASE_COMMAND_STATUS_VALUES!r}")
