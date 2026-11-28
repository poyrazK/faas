from typing import Literal

ObsRuntimeAppTaskClassState = Literal["conflicting", "empty", "missing", "nonempty", "stale", "unavailable"]

OBS_RUNTIME_APP_TASK_CLASS_STATE_VALUES: set[ObsRuntimeAppTaskClassState] = {
    "conflicting",
    "empty",
    "missing",
    "nonempty",
    "stale",
    "unavailable",
}


def check_obs_runtime_app_task_class_state(value: str) -> ObsRuntimeAppTaskClassState:
    if value in OBS_RUNTIME_APP_TASK_CLASS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_CLASS_STATE_VALUES!r}")
