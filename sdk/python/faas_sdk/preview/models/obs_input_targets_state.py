from typing import Literal

ObsInputTargetsState = Literal["degraded", "healthy", "partial", "unknown"]

OBS_INPUT_TARGETS_STATE_VALUES: set[ObsInputTargetsState] = {
    "degraded",
    "healthy",
    "partial",
    "unknown",
}


def check_obs_input_targets_state(value: str) -> ObsInputTargetsState:
    if value in OBS_INPUT_TARGETS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_TARGETS_STATE_VALUES!r}")
