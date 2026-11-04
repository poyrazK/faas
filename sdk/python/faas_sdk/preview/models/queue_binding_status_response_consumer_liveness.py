from typing import Literal

QueueBindingStatusResponseConsumerLiveness = Literal["degraded", "external", "healthy", "not_observed", "stale"]

QUEUE_BINDING_STATUS_RESPONSE_CONSUMER_LIVENESS_VALUES: set[QueueBindingStatusResponseConsumerLiveness] = {
    "degraded",
    "external",
    "healthy",
    "not_observed",
    "stale",
}


def check_queue_binding_status_response_consumer_liveness(value: str) -> QueueBindingStatusResponseConsumerLiveness:
    if value in QUEUE_BINDING_STATUS_RESPONSE_CONSUMER_LIVENESS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {QUEUE_BINDING_STATUS_RESPONSE_CONSUMER_LIVENESS_VALUES!r}"
    )
