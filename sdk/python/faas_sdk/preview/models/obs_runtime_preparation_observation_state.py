from typing import Literal

ObsRuntimePreparationObservationState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_PREPARATION_OBSERVATION_STATE_VALUES: set[ObsRuntimePreparationObservationState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_preparation_observation_state(value: str) -> ObsRuntimePreparationObservationState:
    if value in OBS_RUNTIME_PREPARATION_OBSERVATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PREPARATION_OBSERVATION_STATE_VALUES!r}")
