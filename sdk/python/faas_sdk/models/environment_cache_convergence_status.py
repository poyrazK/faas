from typing import Literal

EnvironmentCacheConvergenceStatus = Literal["converged", "degraded", "pending"]

ENVIRONMENT_CACHE_CONVERGENCE_STATUS_VALUES: set[EnvironmentCacheConvergenceStatus] = {
    "converged",
    "degraded",
    "pending",
}


def check_environment_cache_convergence_status(value: str) -> EnvironmentCacheConvergenceStatus:
    if value in ENVIRONMENT_CACHE_CONVERGENCE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_CACHE_CONVERGENCE_STATUS_VALUES!r}")
