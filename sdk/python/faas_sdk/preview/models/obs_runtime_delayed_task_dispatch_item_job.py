from typing import Literal

ObsRuntimeDelayedTaskDispatchItemJob = Literal["schedd", "schedd-compute"]

OBS_RUNTIME_DELAYED_TASK_DISPATCH_ITEM_JOB_VALUES: set[ObsRuntimeDelayedTaskDispatchItemJob] = {
    "schedd",
    "schedd-compute",
}


def check_obs_runtime_delayed_task_dispatch_item_job(value: str) -> ObsRuntimeDelayedTaskDispatchItemJob:
    if value in OBS_RUNTIME_DELAYED_TASK_DISPATCH_ITEM_JOB_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_DELAYED_TASK_DISPATCH_ITEM_JOB_VALUES!r}"
    )
