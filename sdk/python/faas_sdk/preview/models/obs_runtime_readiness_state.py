from typing import Literal

ObsRuntimeReadinessState = Literal["incomplete", "not_ready", "ready", "unknown"]

OBS_RUNTIME_READINESS_STATE_VALUES: set[ObsRuntimeReadinessState] = {
    "incomplete",
    "not_ready",
    "ready",
    "unknown",
}


def check_obs_runtime_readiness_state(value: str) -> ObsRuntimeReadinessState:
    if value in OBS_RUNTIME_READINESS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_READINESS_STATE_VALUES!r}")
