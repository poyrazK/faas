from typing import Literal

EventExecutionLineageRetryJobAttentionPageFilter = Literal["any", "retention_risk", "stalled"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_ATTENTION_PAGE_FILTER_VALUES: set[
    EventExecutionLineageRetryJobAttentionPageFilter
] = {
    "any",
    "retention_risk",
    "stalled",
}


def check_event_execution_lineage_retry_job_attention_page_filter(
    value: str,
) -> EventExecutionLineageRetryJobAttentionPageFilter:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_ATTENTION_PAGE_FILTER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_ATTENTION_PAGE_FILTER_VALUES!r}"
    )
