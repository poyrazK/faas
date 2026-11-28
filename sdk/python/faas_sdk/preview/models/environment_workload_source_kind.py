from typing import Literal

EnvironmentWorkloadSourceKind = Literal["dockerfile", "function", "image", "source"]

ENVIRONMENT_WORKLOAD_SOURCE_KIND_VALUES: set[EnvironmentWorkloadSourceKind] = {
    "dockerfile",
    "function",
    "image",
    "source",
}


def check_environment_workload_source_kind(value: str) -> EnvironmentWorkloadSourceKind:
    if value in ENVIRONMENT_WORKLOAD_SOURCE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_WORKLOAD_SOURCE_KIND_VALUES!r}")
