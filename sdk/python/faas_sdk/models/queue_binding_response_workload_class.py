from typing import Literal

QueueBindingResponseWorkloadClass = Literal["job", "worker"]

QUEUE_BINDING_RESPONSE_WORKLOAD_CLASS_VALUES: set[QueueBindingResponseWorkloadClass] = {
    "job",
    "worker",
}


def check_queue_binding_response_workload_class(value: str) -> QueueBindingResponseWorkloadClass:
    if value in QUEUE_BINDING_RESPONSE_WORKLOAD_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {QUEUE_BINDING_RESPONSE_WORKLOAD_CLASS_VALUES!r}")
