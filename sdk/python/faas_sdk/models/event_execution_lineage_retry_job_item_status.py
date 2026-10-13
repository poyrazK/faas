from typing import Literal

EventExecutionLineageRetryJobItemStatus = Literal[
    "already_associated", "cancelled", "changed", "conflict", "pending", "repaired", "unavailable", "unresolved"
]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_ITEM_STATUS_VALUES: set[EventExecutionLineageRetryJobItemStatus] = {
    "already_associated",
    "cancelled",
    "changed",
    "conflict",
    "pending",
    "repaired",
    "unavailable",
    "unresolved",
}


def check_event_execution_lineage_retry_job_item_status(value: str) -> EventExecutionLineageRetryJobItemStatus:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_ITEM_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_ITEM_STATUS_VALUES!r}"
    )
