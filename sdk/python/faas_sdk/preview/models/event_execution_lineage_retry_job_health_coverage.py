from typing import Literal

EventExecutionLineageRetryJobHealthCoverage = Literal["bounded_retained_active_lineage_retry_jobs"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_HEALTH_COVERAGE_VALUES: set[EventExecutionLineageRetryJobHealthCoverage] = {
    "bounded_retained_active_lineage_retry_jobs",
}


def check_event_execution_lineage_retry_job_health_coverage(value: str) -> EventExecutionLineageRetryJobHealthCoverage:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_HEALTH_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_HEALTH_COVERAGE_VALUES!r}"
    )
