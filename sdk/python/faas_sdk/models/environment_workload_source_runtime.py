from typing import Literal

EnvironmentWorkloadSourceRuntime = Literal["go124", "go124-alpine", "node22", "node24", "python312", "python313"]

ENVIRONMENT_WORKLOAD_SOURCE_RUNTIME_VALUES: set[EnvironmentWorkloadSourceRuntime] = {
    "go124",
    "go124-alpine",
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_environment_workload_source_runtime(value: str) -> EnvironmentWorkloadSourceRuntime:
    if value in ENVIRONMENT_WORKLOAD_SOURCE_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_WORKLOAD_SOURCE_RUNTIME_VALUES!r}")
