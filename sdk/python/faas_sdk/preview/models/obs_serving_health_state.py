from typing import Literal

ObsServingHealthState = Literal["degraded", "healthy", "not_applicable", "partial"]

OBS_SERVING_HEALTH_STATE_VALUES: set[ObsServingHealthState] = {
    "degraded",
    "healthy",
    "not_applicable",
    "partial",
}


def check_obs_serving_health_state(value: str) -> ObsServingHealthState:
    if value in OBS_SERVING_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_SERVING_HEALTH_STATE_VALUES!r}")
