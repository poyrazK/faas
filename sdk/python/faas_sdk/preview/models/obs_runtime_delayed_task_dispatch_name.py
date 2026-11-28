from typing import Literal

ObsRuntimeDelayedTaskDispatchName = Literal["dead_letter", "failed", "retry", "success"]

OBS_RUNTIME_DELAYED_TASK_DISPATCH_NAME_VALUES: set[ObsRuntimeDelayedTaskDispatchName] = {
    "dead_letter",
    "failed",
    "retry",
    "success",
}


def check_obs_runtime_delayed_task_dispatch_name(value: str) -> ObsRuntimeDelayedTaskDispatchName:
    if value in OBS_RUNTIME_DELAYED_TASK_DISPATCH_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_DELAYED_TASK_DISPATCH_NAME_VALUES!r}")
