from typing import Literal

ObsRuntimePreparationHealthState = Literal["empty", "incomplete", "unknown", "waiting"]

OBS_RUNTIME_PREPARATION_HEALTH_STATE_VALUES: set[ObsRuntimePreparationHealthState] = {
    "empty",
    "incomplete",
    "unknown",
    "waiting",
}


def check_obs_runtime_preparation_health_state(value: str) -> ObsRuntimePreparationHealthState:
    if value in OBS_RUNTIME_PREPARATION_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PREPARATION_HEALTH_STATE_VALUES!r}")
