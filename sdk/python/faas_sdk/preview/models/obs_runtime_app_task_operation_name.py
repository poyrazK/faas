from typing import Literal

ObsRuntimeAppTaskOperationName = Literal["complete", "destroy", "execute", "renew", "restore"]

OBS_RUNTIME_APP_TASK_OPERATION_NAME_VALUES: set[ObsRuntimeAppTaskOperationName] = {
    "complete",
    "destroy",
    "execute",
    "renew",
    "restore",
}


def check_obs_runtime_app_task_operation_name(value: str) -> ObsRuntimeAppTaskOperationName:
    if value in OBS_RUNTIME_APP_TASK_OPERATION_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_OPERATION_NAME_VALUES!r}")
