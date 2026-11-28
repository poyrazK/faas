from typing import Literal

JobRunResponseAggregateStatus = Literal["cancelled", "dead_letter", "failed", "queued", "running", "succeeded"]

JOB_RUN_RESPONSE_AGGREGATE_STATUS_VALUES: set[JobRunResponseAggregateStatus] = {
    "cancelled",
    "dead_letter",
    "failed",
    "queued",
    "running",
    "succeeded",
}


def check_job_run_response_aggregate_status(value: str) -> JobRunResponseAggregateStatus:
    if value in JOB_RUN_RESPONSE_AGGREGATE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_RUN_RESPONSE_AGGREGATE_STATUS_VALUES!r}")
