from typing import Literal

QueueBindingStatusResponseWorkloadClass = Literal["job", "worker"]

QUEUE_BINDING_STATUS_RESPONSE_WORKLOAD_CLASS_VALUES: set[QueueBindingStatusResponseWorkloadClass] = {
    "job",
    "worker",
}


def check_queue_binding_status_response_workload_class(value: str) -> QueueBindingStatusResponseWorkloadClass:
    if value in QUEUE_BINDING_STATUS_RESPONSE_WORKLOAD_CLASS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {QUEUE_BINDING_STATUS_RESPONSE_WORKLOAD_CLASS_VALUES!r}"
    )
