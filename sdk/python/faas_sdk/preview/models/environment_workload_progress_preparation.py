from typing import Literal

EnvironmentWorkloadProgressPreparation = Literal["complete", "failed", "pending", "unknown"]

ENVIRONMENT_WORKLOAD_PROGRESS_PREPARATION_VALUES: set[EnvironmentWorkloadProgressPreparation] = {
    "complete",
    "failed",
    "pending",
    "unknown",
}


def check_environment_workload_progress_preparation(value: str) -> EnvironmentWorkloadProgressPreparation:
    if value in ENVIRONMENT_WORKLOAD_PROGRESS_PREPARATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_WORKLOAD_PROGRESS_PREPARATION_VALUES!r}")
