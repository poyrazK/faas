from typing import Literal

EventExecutionLineageRetryJobAttentionOptionsAttention = Literal["", "any", "retention_risk", "stalled"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_ATTENTION_OPTIONS_ATTENTION_VALUES: set[
    EventExecutionLineageRetryJobAttentionOptionsAttention
] = {
    "",
    "any",
    "retention_risk",
    "stalled",
}


def check_event_execution_lineage_retry_job_attention_options_attention(
    value: str,
) -> EventExecutionLineageRetryJobAttentionOptionsAttention:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_ATTENTION_OPTIONS_ATTENTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_ATTENTION_OPTIONS_ATTENTION_VALUES!r}"
    )
