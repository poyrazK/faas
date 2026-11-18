from typing import Literal

ObsRuntimeHandoffHealthState = Literal["empty", "incomplete", "unknown", "waiting"]

OBS_RUNTIME_HANDOFF_HEALTH_STATE_VALUES: set[ObsRuntimeHandoffHealthState] = {
    "empty",
    "incomplete",
    "unknown",
    "waiting",
}


def check_obs_runtime_handoff_health_state(value: str) -> ObsRuntimeHandoffHealthState:
    if value in OBS_RUNTIME_HANDOFF_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HANDOFF_HEALTH_STATE_VALUES!r}")
