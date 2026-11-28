from typing import Literal

EnvironmentWorkloadProgressQualification = Literal["complete", "failed", "pending", "unknown"]

ENVIRONMENT_WORKLOAD_PROGRESS_QUALIFICATION_VALUES: set[EnvironmentWorkloadProgressQualification] = {
    "complete",
    "failed",
    "pending",
    "unknown",
}


def check_environment_workload_progress_qualification(value: str) -> EnvironmentWorkloadProgressQualification:
    if value in ENVIRONMENT_WORKLOAD_PROGRESS_QUALIFICATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_WORKLOAD_PROGRESS_QUALIFICATION_VALUES!r}"
    )
