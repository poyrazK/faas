from typing import Literal

EventReceiptExecutionResponseState = Literal[
    "cancelled", "completed", "dead_letter", "dispatching", "expired", "failed", "pending", "superseded"
]

EVENT_RECEIPT_EXECUTION_RESPONSE_STATE_VALUES: set[EventReceiptExecutionResponseState] = {
    "cancelled",
    "completed",
    "dead_letter",
    "dispatching",
    "expired",
    "failed",
    "pending",
    "superseded",
}


def check_event_receipt_execution_response_state(value: str) -> EventReceiptExecutionResponseState:
    if value in EVENT_RECEIPT_EXECUTION_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_EXECUTION_RESPONSE_STATE_VALUES!r}")
