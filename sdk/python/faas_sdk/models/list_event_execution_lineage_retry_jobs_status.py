from typing import Literal

ListEventExecutionLineageRetryJobsStatus = Literal["cancelled", "completed", "pending"]

LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOBS_STATUS_VALUES: set[ListEventExecutionLineageRetryJobsStatus] = {
    "cancelled",
    "completed",
    "pending",
}


def check_list_event_execution_lineage_retry_jobs_status(value: str) -> ListEventExecutionLineageRetryJobsStatus:
    if value in LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOBS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOBS_STATUS_VALUES!r}"
    )
