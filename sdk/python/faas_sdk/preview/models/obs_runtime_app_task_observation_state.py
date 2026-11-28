from typing import Literal

ObsRuntimeAppTaskObservationState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_APP_TASK_OBSERVATION_STATE_VALUES: set[ObsRuntimeAppTaskObservationState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_app_task_observation_state(value: str) -> ObsRuntimeAppTaskObservationState:
    if value in OBS_RUNTIME_APP_TASK_OBSERVATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_OBSERVATION_STATE_VALUES!r}")
