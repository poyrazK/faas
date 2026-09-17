from typing import Literal

CreateQueueBindingRequestWorkloadClass = Literal["job", "worker"]

CREATE_QUEUE_BINDING_REQUEST_WORKLOAD_CLASS_VALUES: set[CreateQueueBindingRequestWorkloadClass] = {
    "job",
    "worker",
}


def check_create_queue_binding_request_workload_class(value: str) -> CreateQueueBindingRequestWorkloadClass:
    if value in CREATE_QUEUE_BINDING_REQUEST_WORKLOAD_CLASS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_QUEUE_BINDING_REQUEST_WORKLOAD_CLASS_VALUES!r}"
    )
