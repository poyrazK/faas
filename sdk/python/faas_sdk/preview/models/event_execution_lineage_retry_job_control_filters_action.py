from typing import Literal

EventExecutionLineageRetryJobControlFiltersAction = Literal["", "cancel", "pause", "resume"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_FILTERS_ACTION_VALUES: set[
    EventExecutionLineageRetryJobControlFiltersAction
] = {
    "",
    "cancel",
    "pause",
    "resume",
}


def check_event_execution_lineage_retry_job_control_filters_action(
    value: str,
) -> EventExecutionLineageRetryJobControlFiltersAction:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_FILTERS_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_FILTERS_ACTION_VALUES!r}"
    )
