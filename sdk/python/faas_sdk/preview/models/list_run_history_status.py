from typing import Literal

ListRunHistoryStatus = Literal[
    "cancelled", "failed", "out_of_memory", "queued", "restoring", "running", "succeeded", "timed_out"
]

LIST_RUN_HISTORY_STATUS_VALUES: set[ListRunHistoryStatus] = {
    "cancelled",
    "failed",
    "out_of_memory",
    "queued",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
}


def check_list_run_history_status(value: str) -> ListRunHistoryStatus:
    if value in LIST_RUN_HISTORY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_RUN_HISTORY_STATUS_VALUES!r}")
