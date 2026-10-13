from typing import Literal

EventExecutionLineageRetryJobStatus = Literal["completed", "pending"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_STATUS_VALUES: set[EventExecutionLineageRetryJobStatus] = {
    "completed",
    "pending",
}


def check_event_execution_lineage_retry_job_status(value: str) -> EventExecutionLineageRetryJobStatus:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_STATUS_VALUES!r}")
