from typing import Literal

QueueBindingResponseMode = Literal["pull", "push"]

QUEUE_BINDING_RESPONSE_MODE_VALUES: set[QueueBindingResponseMode] = {
    "pull",
    "push",
}


def check_queue_binding_response_mode(value: str) -> QueueBindingResponseMode:
    if value in QUEUE_BINDING_RESPONSE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {QUEUE_BINDING_RESPONSE_MODE_VALUES!r}")
