from typing import Literal

ProjectReleaseWorkerStatus = Literal["active", "building", "conflicted", "failed", "ready", "restored"]

PROJECT_RELEASE_WORKER_STATUS_VALUES: set[ProjectReleaseWorkerStatus] = {
    "active",
    "building",
    "conflicted",
    "failed",
    "ready",
    "restored",
}


def check_project_release_worker_status(value: str) -> ProjectReleaseWorkerStatus:
    if value in PROJECT_RELEASE_WORKER_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_RELEASE_WORKER_STATUS_VALUES!r}")
