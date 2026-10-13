from typing import Literal

EventExecutionLineageRetryJobSummaryStatus = Literal["cancelled", "completed", "paused", "pending"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_SUMMARY_STATUS_VALUES: set[EventExecutionLineageRetryJobSummaryStatus] = {
    "cancelled",
    "completed",
    "paused",
    "pending",
}


def check_event_execution_lineage_retry_job_summary_status(value: str) -> EventExecutionLineageRetryJobSummaryStatus:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_SUMMARY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_SUMMARY_STATUS_VALUES!r}"
    )
