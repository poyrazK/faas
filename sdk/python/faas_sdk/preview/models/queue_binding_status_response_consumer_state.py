from typing import Literal

QueueBindingStatusResponseConsumerState = Literal["active", "external", "not_configured", "paused"]

QUEUE_BINDING_STATUS_RESPONSE_CONSUMER_STATE_VALUES: set[QueueBindingStatusResponseConsumerState] = {
    "active",
    "external",
    "not_configured",
    "paused",
}


def check_queue_binding_status_response_consumer_state(value: str) -> QueueBindingStatusResponseConsumerState:
    if value in QUEUE_BINDING_STATUS_RESPONSE_CONSUMER_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {QUEUE_BINDING_STATUS_RESPONSE_CONSUMER_STATE_VALUES!r}"
    )
