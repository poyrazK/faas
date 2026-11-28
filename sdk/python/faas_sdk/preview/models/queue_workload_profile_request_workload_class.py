from typing import Literal

QueueWorkloadProfileRequestWorkloadClass = Literal["job", "worker"]

QUEUE_WORKLOAD_PROFILE_REQUEST_WORKLOAD_CLASS_VALUES: set[QueueWorkloadProfileRequestWorkloadClass] = {
    "job",
    "worker",
}


def check_queue_workload_profile_request_workload_class(value: str) -> QueueWorkloadProfileRequestWorkloadClass:
    if value in QUEUE_WORKLOAD_PROFILE_REQUEST_WORKLOAD_CLASS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {QUEUE_WORKLOAD_PROFILE_REQUEST_WORKLOAD_CLASS_VALUES!r}"
    )
