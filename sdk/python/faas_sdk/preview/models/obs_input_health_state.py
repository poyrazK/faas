from typing import Literal

ObsInputHealthState = Literal["degraded", "healthy", "partial", "unknown"]

OBS_INPUT_HEALTH_STATE_VALUES: set[ObsInputHealthState] = {
    "degraded",
    "healthy",
    "partial",
    "unknown",
}


def check_obs_input_health_state(value: str) -> ObsInputHealthState:
    if value in OBS_INPUT_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_HEALTH_STATE_VALUES!r}")
