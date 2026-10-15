from typing import Literal

EventExecutionLineageRetryJobControlReceiptAction = Literal["cancel", "pause", "resume"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_ACTION_VALUES: set[
    EventExecutionLineageRetryJobControlReceiptAction
] = {
    "cancel",
    "pause",
    "resume",
}


def check_event_execution_lineage_retry_job_control_receipt_action(
    value: str,
) -> EventExecutionLineageRetryJobControlReceiptAction:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_ACTION_VALUES!r}"
    )
