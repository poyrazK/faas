from typing import Literal

EventExecutionLineageRetryJobStatus = Literal["cancelled", "completed", "paused", "pending"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_STATUS_VALUES: set[EventExecutionLineageRetryJobStatus] = {
    "cancelled",
    "completed",
    "paused",
    "pending",
}


def check_event_execution_lineage_retry_job_status(value: str) -> EventExecutionLineageRetryJobStatus:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_STATUS_VALUES!r}")
