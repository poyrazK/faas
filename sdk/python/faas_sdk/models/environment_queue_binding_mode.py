from typing import Literal

EnvironmentQueueBindingMode = Literal["pull", "push"]

ENVIRONMENT_QUEUE_BINDING_MODE_VALUES: set[EnvironmentQueueBindingMode] = {
    "pull",
    "push",
}


def check_environment_queue_binding_mode(value: str) -> EnvironmentQueueBindingMode:
    if value in ENVIRONMENT_QUEUE_BINDING_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_QUEUE_BINDING_MODE_VALUES!r}")
