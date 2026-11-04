from typing import Literal

EventExecutionLineageRetryJobHealthSampleStatus = Literal["paused", "pending"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_HEALTH_SAMPLE_STATUS_VALUES: set[EventExecutionLineageRetryJobHealthSampleStatus] = {
    "paused",
    "pending",
}


def check_event_execution_lineage_retry_job_health_sample_status(
    value: str,
) -> EventExecutionLineageRetryJobHealthSampleStatus:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_HEALTH_SAMPLE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_HEALTH_SAMPLE_STATUS_VALUES!r}"
    )
