from typing import Literal

ProjectEnvironmentQueueBindingMode = Literal["pull", "push"]

PROJECT_ENVIRONMENT_QUEUE_BINDING_MODE_VALUES: set[ProjectEnvironmentQueueBindingMode] = {
    "pull",
    "push",
}


def check_project_environment_queue_binding_mode(value: str) -> ProjectEnvironmentQueueBindingMode:
    if value in PROJECT_ENVIRONMENT_QUEUE_BINDING_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_QUEUE_BINDING_MODE_VALUES!r}")
