from typing import Literal

EnvironmentWorkloadProgressServing = Literal["complete", "failed", "pending", "unknown"]

ENVIRONMENT_WORKLOAD_PROGRESS_SERVING_VALUES: set[EnvironmentWorkloadProgressServing] = {
    "complete",
    "failed",
    "pending",
    "unknown",
}


def check_environment_workload_progress_serving(value: str) -> EnvironmentWorkloadProgressServing:
    if value in ENVIRONMENT_WORKLOAD_PROGRESS_SERVING_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_WORKLOAD_PROGRESS_SERVING_VALUES!r}")
