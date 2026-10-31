from typing import Literal

ProjectWorkerHealthStatus = Literal["healthy", "insufficient_evidence", "regressed"]

PROJECT_WORKER_HEALTH_STATUS_VALUES: set[ProjectWorkerHealthStatus] = {
    "healthy",
    "insufficient_evidence",
    "regressed",
}


def check_project_worker_health_status(value: str) -> ProjectWorkerHealthStatus:
    if value in PROJECT_WORKER_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_WORKER_HEALTH_STATUS_VALUES!r}")
