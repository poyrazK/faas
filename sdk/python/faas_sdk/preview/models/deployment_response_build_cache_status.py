from typing import Literal

DeploymentResponseBuildCacheStatus = Literal["hit", "invalidated", "miss"]

DEPLOYMENT_RESPONSE_BUILD_CACHE_STATUS_VALUES: set[DeploymentResponseBuildCacheStatus] = {
    "hit",
    "invalidated",
    "miss",
}


def check_deployment_response_build_cache_status(value: str) -> DeploymentResponseBuildCacheStatus:
    if value in DEPLOYMENT_RESPONSE_BUILD_CACHE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RESPONSE_BUILD_CACHE_STATUS_VALUES!r}")
