from typing import Literal

UpdateQueueBindingRequestMode = Literal["pull", "push"]

UPDATE_QUEUE_BINDING_REQUEST_MODE_VALUES: set[UpdateQueueBindingRequestMode] = {
    "pull",
    "push",
}


def check_update_queue_binding_request_mode(value: str) -> UpdateQueueBindingRequestMode:
    if value in UPDATE_QUEUE_BINDING_REQUEST_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_QUEUE_BINDING_REQUEST_MODE_VALUES!r}")
