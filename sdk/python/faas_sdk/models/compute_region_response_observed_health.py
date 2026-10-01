from typing import Literal

ComputeRegionResponseObservedHealth = Literal["degraded", "healthy", "unavailable", "unknown"]

COMPUTE_REGION_RESPONSE_OBSERVED_HEALTH_VALUES: set[ComputeRegionResponseObservedHealth] = {
    "degraded",
    "healthy",
    "unavailable",
    "unknown",
}


def check_compute_region_response_observed_health(value: str) -> ComputeRegionResponseObservedHealth:
    if value in COMPUTE_REGION_RESPONSE_OBSERVED_HEALTH_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMPUTE_REGION_RESPONSE_OBSERVED_HEALTH_VALUES!r}")
