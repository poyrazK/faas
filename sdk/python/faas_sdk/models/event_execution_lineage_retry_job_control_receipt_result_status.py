from typing import Literal

EventExecutionLineageRetryJobControlReceiptResultStatus = Literal["cancelled", "completed", "paused", "pending"]

EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_RESULT_STATUS_VALUES: set[
    EventExecutionLineageRetryJobControlReceiptResultStatus
] = {
    "cancelled",
    "completed",
    "paused",
    "pending",
}


def check_event_execution_lineage_retry_job_control_receipt_result_status(
    value: str,
) -> EventExecutionLineageRetryJobControlReceiptResultStatus:
    if value in EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_RESULT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_LINEAGE_RETRY_JOB_CONTROL_RECEIPT_RESULT_STATUS_VALUES!r}"
    )
