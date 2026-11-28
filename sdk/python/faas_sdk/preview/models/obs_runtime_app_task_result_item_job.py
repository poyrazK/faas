from typing import Literal

ObsRuntimeAppTaskResultItemJob = Literal["schedd", "schedd-compute"]

OBS_RUNTIME_APP_TASK_RESULT_ITEM_JOB_VALUES: set[ObsRuntimeAppTaskResultItemJob] = {
    "schedd",
    "schedd-compute",
}


def check_obs_runtime_app_task_result_item_job(value: str) -> ObsRuntimeAppTaskResultItemJob:
    if value in OBS_RUNTIME_APP_TASK_RESULT_ITEM_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_RESULT_ITEM_JOB_VALUES!r}")
