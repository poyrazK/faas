from typing import Literal

ObsRuntimeBuilderWorkerState = Literal[
    "conflicting", "draining", "missing", "overdue", "stale", "unavailable", "waiting", "working"
]

OBS_RUNTIME_BUILDER_WORKER_STATE_VALUES: set[ObsRuntimeBuilderWorkerState] = {
    "conflicting",
    "draining",
    "missing",
    "overdue",
    "stale",
    "unavailable",
    "waiting",
    "working",
}


def check_obs_runtime_builder_worker_state(value: str) -> ObsRuntimeBuilderWorkerState:
    if value in OBS_RUNTIME_BUILDER_WORKER_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDER_WORKER_STATE_VALUES!r}")
