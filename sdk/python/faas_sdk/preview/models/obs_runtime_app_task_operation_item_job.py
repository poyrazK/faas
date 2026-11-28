from typing import Literal

ObsRuntimeAppTaskOperationItemJob = Literal["schedd", "schedd-compute"]

OBS_RUNTIME_APP_TASK_OPERATION_ITEM_JOB_VALUES: set[ObsRuntimeAppTaskOperationItemJob] = {
    "schedd",
    "schedd-compute",
}


def check_obs_runtime_app_task_operation_item_job(value: str) -> ObsRuntimeAppTaskOperationItemJob:
    if value in OBS_RUNTIME_APP_TASK_OPERATION_ITEM_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_OPERATION_ITEM_JOB_VALUES!r}")
