from typing import Literal

ObsHealthSignalState = Literal["healthy", "no_samples", "stale", "unavailable"]

OBS_HEALTH_SIGNAL_STATE_VALUES: set[ObsHealthSignalState] = {
    "healthy",
    "no_samples",
    "stale",
    "unavailable",
}


def check_obs_health_signal_state(value: str) -> ObsHealthSignalState:
    if value in OBS_HEALTH_SIGNAL_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_HEALTH_SIGNAL_STATE_VALUES!r}")
