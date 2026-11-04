from typing import Literal

EnvironmentWorkloadProgressActivation = Literal["complete", "failed", "pending", "unknown"]

ENVIRONMENT_WORKLOAD_PROGRESS_ACTIVATION_VALUES: set[EnvironmentWorkloadProgressActivation] = {
    "complete",
    "failed",
    "pending",
    "unknown",
}


def check_environment_workload_progress_activation(value: str) -> EnvironmentWorkloadProgressActivation:
    if value in ENVIRONMENT_WORKLOAD_PROGRESS_ACTIVATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_WORKLOAD_PROGRESS_ACTIVATION_VALUES!r}")
