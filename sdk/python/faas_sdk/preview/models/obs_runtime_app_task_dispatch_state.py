from typing import Literal

ObsRuntimeAppTaskDispatchState = Literal[
    "conflicting", "disabled", "missing", "running", "stale", "stopped", "unavailable"
]

OBS_RUNTIME_APP_TASK_DISPATCH_STATE_VALUES: set[ObsRuntimeAppTaskDispatchState] = {
    "conflicting",
    "disabled",
    "missing",
    "running",
    "stale",
    "stopped",
    "unavailable",
}


def check_obs_runtime_app_task_dispatch_state(value: str) -> ObsRuntimeAppTaskDispatchState:
    if value in OBS_RUNTIME_APP_TASK_DISPATCH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_DISPATCH_STATE_VALUES!r}")
