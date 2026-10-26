from typing import Literal

EnvironmentWorkloadProgressBuildCacheStatus = Literal["hit", "invalidated", "miss"]

ENVIRONMENT_WORKLOAD_PROGRESS_BUILD_CACHE_STATUS_VALUES: set[EnvironmentWorkloadProgressBuildCacheStatus] = {
    "hit",
    "invalidated",
    "miss",
}


def check_environment_workload_progress_build_cache_status(value: str) -> EnvironmentWorkloadProgressBuildCacheStatus:
    if value in ENVIRONMENT_WORKLOAD_PROGRESS_BUILD_CACHE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_WORKLOAD_PROGRESS_BUILD_CACHE_STATUS_VALUES!r}"
    )
