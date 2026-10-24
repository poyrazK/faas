from typing import Literal

ScanProjectProjectWorkerHealth = Literal["required"]

SCAN_PROJECT_PROJECT_WORKER_HEALTH_VALUES: set[ScanProjectProjectWorkerHealth] = {
    "required",
}


def check_scan_project_project_worker_health(value: str) -> ScanProjectProjectWorkerHealth:
    if value in SCAN_PROJECT_PROJECT_WORKER_HEALTH_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCAN_PROJECT_PROJECT_WORKER_HEALTH_VALUES!r}")
