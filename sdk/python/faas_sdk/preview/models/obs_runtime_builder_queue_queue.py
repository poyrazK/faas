from typing import Literal

ObsRuntimeBuilderQueueQueue = Literal["build", "cancel"]

OBS_RUNTIME_BUILDER_QUEUE_QUEUE_VALUES: set[ObsRuntimeBuilderQueueQueue] = {
    "build",
    "cancel",
}


def check_obs_runtime_builder_queue_queue(value: str) -> ObsRuntimeBuilderQueueQueue:
    if value in OBS_RUNTIME_BUILDER_QUEUE_QUEUE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDER_QUEUE_QUEUE_VALUES!r}")
