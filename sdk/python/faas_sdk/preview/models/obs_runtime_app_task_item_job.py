from typing import Literal

ObsRuntimeAppTaskItemJob = Literal["schedd", "schedd-compute"]

OBS_RUNTIME_APP_TASK_ITEM_JOB_VALUES: set[ObsRuntimeAppTaskItemJob] = {
    "schedd",
    "schedd-compute",
}


def check_obs_runtime_app_task_item_job(value: str) -> ObsRuntimeAppTaskItemJob:
    if value in OBS_RUNTIME_APP_TASK_ITEM_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_ITEM_JOB_VALUES!r}")
