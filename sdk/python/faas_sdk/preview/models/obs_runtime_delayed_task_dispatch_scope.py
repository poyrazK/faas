from typing import Literal

ObsRuntimeDelayedTaskDispatchScope = Literal["process_local_counter_observations"]

OBS_RUNTIME_DELAYED_TASK_DISPATCH_SCOPE_VALUES: set[ObsRuntimeDelayedTaskDispatchScope] = {
    "process_local_counter_observations",
}


def check_obs_runtime_delayed_task_dispatch_scope(value: str) -> ObsRuntimeDelayedTaskDispatchScope:
    if value in OBS_RUNTIME_DELAYED_TASK_DISPATCH_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_DELAYED_TASK_DISPATCH_SCOPE_VALUES!r}")
