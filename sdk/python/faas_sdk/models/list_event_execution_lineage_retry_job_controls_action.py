from typing import Literal

ListEventExecutionLineageRetryJobControlsAction = Literal["", "cancel", "pause", "resume"]

LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROLS_ACTION_VALUES: set[ListEventExecutionLineageRetryJobControlsAction] = {
    "",
    "cancel",
    "pause",
    "resume",
}


def check_list_event_execution_lineage_retry_job_controls_action(
    value: str,
) -> ListEventExecutionLineageRetryJobControlsAction:
    if value in LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROLS_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROLS_ACTION_VALUES!r}"
    )
