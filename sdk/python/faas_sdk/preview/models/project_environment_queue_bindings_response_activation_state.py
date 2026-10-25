from typing import Literal

ProjectEnvironmentQueueBindingsResponseActivationState = Literal["unavailable"]

PROJECT_ENVIRONMENT_QUEUE_BINDINGS_RESPONSE_ACTIVATION_STATE_VALUES: set[
    ProjectEnvironmentQueueBindingsResponseActivationState
] = {
    "unavailable",
}


def check_project_environment_queue_bindings_response_activation_state(
    value: str,
) -> ProjectEnvironmentQueueBindingsResponseActivationState:
    if value in PROJECT_ENVIRONMENT_QUEUE_BINDINGS_RESPONSE_ACTIVATION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_QUEUE_BINDINGS_RESPONSE_ACTIVATION_STATE_VALUES!r}"
    )
