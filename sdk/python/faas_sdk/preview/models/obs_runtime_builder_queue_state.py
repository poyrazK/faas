from typing import Literal

ObsRuntimeBuilderQueueState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_BUILDER_QUEUE_STATE_VALUES: set[ObsRuntimeBuilderQueueState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_builder_queue_state(value: str) -> ObsRuntimeBuilderQueueState:
    if value in OBS_RUNTIME_BUILDER_QUEUE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDER_QUEUE_STATE_VALUES!r}")
