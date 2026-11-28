from typing import Literal

ObsRuntimeDelayedTaskDispatchState = Literal["baseline", "incomplete", "increased", "unchanged", "unknown"]

OBS_RUNTIME_DELAYED_TASK_DISPATCH_STATE_VALUES: set[ObsRuntimeDelayedTaskDispatchState] = {
    "baseline",
    "incomplete",
    "increased",
    "unchanged",
    "unknown",
}


def check_obs_runtime_delayed_task_dispatch_state(value: str) -> ObsRuntimeDelayedTaskDispatchState:
    if value in OBS_RUNTIME_DELAYED_TASK_DISPATCH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_DELAYED_TASK_DISPATCH_STATE_VALUES!r}")
