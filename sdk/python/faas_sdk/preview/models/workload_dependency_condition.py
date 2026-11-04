from typing import Literal

WorkloadDependencyCondition = Literal["completed_successfully", "healthy", "started"]

WORKLOAD_DEPENDENCY_CONDITION_VALUES: set[WorkloadDependencyCondition] = {
    "completed_successfully",
    "healthy",
    "started",
}


def check_workload_dependency_condition(value: str) -> WorkloadDependencyCondition:
    if value in WORKLOAD_DEPENDENCY_CONDITION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKLOAD_DEPENDENCY_CONDITION_VALUES!r}")
