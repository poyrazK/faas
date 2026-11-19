from typing import Literal

ObsRuntimeLoopHealthState = Literal["incomplete", "no_reported_stall", "stalled", "unknown"]

OBS_RUNTIME_LOOP_HEALTH_STATE_VALUES: set[ObsRuntimeLoopHealthState] = {
    "incomplete",
    "no_reported_stall",
    "stalled",
    "unknown",
}


def check_obs_runtime_loop_health_state(value: str) -> ObsRuntimeLoopHealthState:
    if value in OBS_RUNTIME_LOOP_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_LOOP_HEALTH_STATE_VALUES!r}")
