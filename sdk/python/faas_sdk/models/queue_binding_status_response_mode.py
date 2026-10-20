from typing import Literal

QueueBindingStatusResponseMode = Literal["pull", "push"]

QUEUE_BINDING_STATUS_RESPONSE_MODE_VALUES: set[QueueBindingStatusResponseMode] = {
    "pull",
    "push",
}


def check_queue_binding_status_response_mode(value: str) -> QueueBindingStatusResponseMode:
    if value in QUEUE_BINDING_STATUS_RESPONSE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {QUEUE_BINDING_STATUS_RESPONSE_MODE_VALUES!r}")
