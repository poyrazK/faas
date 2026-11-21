from typing import Literal

ObsRuntimeAppTaskOperationsScope = Literal["process_local_counter_observations"]

OBS_RUNTIME_APP_TASK_OPERATIONS_SCOPE_VALUES: set[ObsRuntimeAppTaskOperationsScope] = {
    "process_local_counter_observations",
}


def check_obs_runtime_app_task_operations_scope(value: str) -> ObsRuntimeAppTaskOperationsScope:
    if value in OBS_RUNTIME_APP_TASK_OPERATIONS_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_OPERATIONS_SCOPE_VALUES!r}")
