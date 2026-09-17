from typing import Literal

CreateQueueBindingRequestMode = Literal["pull", "push"]

CREATE_QUEUE_BINDING_REQUEST_MODE_VALUES: set[CreateQueueBindingRequestMode] = {
    "pull",
    "push",
}


def check_create_queue_binding_request_mode(value: str) -> CreateQueueBindingRequestMode:
    if value in CREATE_QUEUE_BINDING_REQUEST_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_QUEUE_BINDING_REQUEST_MODE_VALUES!r}")
