from typing import Literal

RunHistoryEntryStatus = Literal[
    "cancelled", "failed", "out_of_memory", "queued", "restoring", "running", "succeeded", "timed_out"
]

RUN_HISTORY_ENTRY_STATUS_VALUES: set[RunHistoryEntryStatus] = {
    "cancelled",
    "failed",
    "out_of_memory",
    "queued",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
}


def check_run_history_entry_status(value: str) -> RunHistoryEntryStatus:
    if value in RUN_HISTORY_ENTRY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUN_HISTORY_ENTRY_STATUS_VALUES!r}")
