from typing import Literal

ObsRuntimeDelayedTaskDispatchSignalState = Literal[
    "baseline", "conflicting", "increased", "missing", "stale", "unavailable", "unchanged"
]

OBS_RUNTIME_DELAYED_TASK_DISPATCH_SIGNAL_STATE_VALUES: set[ObsRuntimeDelayedTaskDispatchSignalState] = {
    "baseline",
    "conflicting",
    "increased",
    "missing",
    "stale",
    "unavailable",
    "unchanged",
}


def check_obs_runtime_delayed_task_dispatch_signal_state(value: str) -> ObsRuntimeDelayedTaskDispatchSignalState:
    if value in OBS_RUNTIME_DELAYED_TASK_DISPATCH_SIGNAL_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_DELAYED_TASK_DISPATCH_SIGNAL_STATE_VALUES!r}"
    )
