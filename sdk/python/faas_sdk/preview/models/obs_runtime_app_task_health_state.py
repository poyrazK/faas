from typing import Literal

ObsRuntimeAppTaskHealthState = Literal["empty", "incomplete", "nonempty", "unknown"]

OBS_RUNTIME_APP_TASK_HEALTH_STATE_VALUES: set[ObsRuntimeAppTaskHealthState] = {
    "empty",
    "incomplete",
    "nonempty",
    "unknown",
}


def check_obs_runtime_app_task_health_state(value: str) -> ObsRuntimeAppTaskHealthState:
    if value in OBS_RUNTIME_APP_TASK_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_HEALTH_STATE_VALUES!r}")
