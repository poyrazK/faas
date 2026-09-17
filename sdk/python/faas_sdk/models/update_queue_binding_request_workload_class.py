from typing import Literal

UpdateQueueBindingRequestWorkloadClass = Literal["job", "worker"]

UPDATE_QUEUE_BINDING_REQUEST_WORKLOAD_CLASS_VALUES: set[UpdateQueueBindingRequestWorkloadClass] = {
    "job",
    "worker",
}


def check_update_queue_binding_request_workload_class(value: str) -> UpdateQueueBindingRequestWorkloadClass:
    if value in UPDATE_QUEUE_BINDING_REQUEST_WORKLOAD_CLASS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_QUEUE_BINDING_REQUEST_WORKLOAD_CLASS_VALUES!r}"
    )
