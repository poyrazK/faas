from typing import Literal

ObsRuntimeAppTaskItemState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_APP_TASK_ITEM_STATE_VALUES: set[ObsRuntimeAppTaskItemState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_app_task_item_state(value: str) -> ObsRuntimeAppTaskItemState:
    if value in OBS_RUNTIME_APP_TASK_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_ITEM_STATE_VALUES!r}")
