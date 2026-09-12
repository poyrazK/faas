from typing import Literal

ListExecutionsStatus = Literal[
    "cancelled", "failed", "out_of_memory", "queued", "restoring", "running", "succeeded", "timed_out"
]

LIST_EXECUTIONS_STATUS_VALUES: set[ListExecutionsStatus] = {
    "cancelled",
    "failed",
    "out_of_memory",
    "queued",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
}


def check_list_executions_status(value: str) -> ListExecutionsStatus:
    if value in LIST_EXECUTIONS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_EXECUTIONS_STATUS_VALUES!r}")
