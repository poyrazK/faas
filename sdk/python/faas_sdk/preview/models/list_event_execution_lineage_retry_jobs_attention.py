from typing import Literal

ListEventExecutionLineageRetryJobsAttention = Literal["", "any", "retention_risk", "stalled"]

LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOBS_ATTENTION_VALUES: set[ListEventExecutionLineageRetryJobsAttention] = {
    "",
    "any",
    "retention_risk",
    "stalled",
}


def check_list_event_execution_lineage_retry_jobs_attention(value: str) -> ListEventExecutionLineageRetryJobsAttention:
    if value in LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOBS_ATTENTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOBS_ATTENTION_VALUES!r}"
    )
