from typing import Literal

ObsRuntimeAppTaskOperationsState = Literal["baseline", "incomplete", "increased", "unchanged", "unknown"]

OBS_RUNTIME_APP_TASK_OPERATIONS_STATE_VALUES: set[ObsRuntimeAppTaskOperationsState] = {
    "baseline",
    "incomplete",
    "increased",
    "unchanged",
    "unknown",
}


def check_obs_runtime_app_task_operations_state(value: str) -> ObsRuntimeAppTaskOperationsState:
    if value in OBS_RUNTIME_APP_TASK_OPERATIONS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_OPERATIONS_STATE_VALUES!r}")
