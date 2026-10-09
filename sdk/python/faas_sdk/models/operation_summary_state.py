from typing import Literal

OperationSummaryState = Literal["accepted", "cancelled", "failed", "requires_reconciliation", "running", "succeeded"]

OPERATION_SUMMARY_STATE_VALUES: set[OperationSummaryState] = {
    "accepted",
    "cancelled",
    "failed",
    "requires_reconciliation",
    "running",
    "succeeded",
}


def check_operation_summary_state(value: str) -> OperationSummaryState:
    if value in OPERATION_SUMMARY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_SUMMARY_STATE_VALUES!r}")
