from typing import Literal

EnvironmentCachePurgeStatusStatus = Literal["completed", "degraded", "pending"]

ENVIRONMENT_CACHE_PURGE_STATUS_STATUS_VALUES: set[EnvironmentCachePurgeStatusStatus] = {
    "completed",
    "degraded",
    "pending",
}


def check_environment_cache_purge_status_status(value: str) -> EnvironmentCachePurgeStatusStatus:
    if value in ENVIRONMENT_CACHE_PURGE_STATUS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_CACHE_PURGE_STATUS_STATUS_VALUES!r}")
