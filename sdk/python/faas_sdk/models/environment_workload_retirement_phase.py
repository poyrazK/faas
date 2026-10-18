from typing import Literal

EnvironmentWorkloadRetirementPhase = Literal["cancelled", "planned", "recovered", "recovering", "retired", "retiring"]

ENVIRONMENT_WORKLOAD_RETIREMENT_PHASE_VALUES: set[EnvironmentWorkloadRetirementPhase] = {
    "cancelled",
    "planned",
    "recovered",
    "recovering",
    "retired",
    "retiring",
}


def check_environment_workload_retirement_phase(value: str) -> EnvironmentWorkloadRetirementPhase:
    if value in ENVIRONMENT_WORKLOAD_RETIREMENT_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_WORKLOAD_RETIREMENT_PHASE_VALUES!r}")
