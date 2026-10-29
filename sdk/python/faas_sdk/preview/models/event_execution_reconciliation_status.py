from typing import Literal

EventExecutionReconciliationStatus = Literal["completed", "not_requested", "not_required", "waiting_for_lineage"]

EVENT_EXECUTION_RECONCILIATION_STATUS_VALUES: set[EventExecutionReconciliationStatus] = {
    "completed",
    "not_requested",
    "not_required",
    "waiting_for_lineage",
}


def check_event_execution_reconciliation_status(value: str) -> EventExecutionReconciliationStatus:
    if value in EVENT_EXECUTION_RECONCILIATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_EXECUTION_RECONCILIATION_STATUS_VALUES!r}")
