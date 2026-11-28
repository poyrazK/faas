from typing import Literal

EventExecutionLineageRetryJobAttentionPageCoverage = Literal["live_matching_retry_jobs_page"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_ATTENTION_PAGE_COVERAGE_VALUES: set[
    EventExecutionLineageRetryJobAttentionPageCoverage
] = {
    "live_matching_retry_jobs_page",
}


def check_event_execution_lineage_retry_job_attention_page_coverage(
    value: str,
) -> EventExecutionLineageRetryJobAttentionPageCoverage:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_ATTENTION_PAGE_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_ATTENTION_PAGE_COVERAGE_VALUES!r}"
    )
