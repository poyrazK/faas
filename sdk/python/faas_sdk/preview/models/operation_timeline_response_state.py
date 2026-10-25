from typing import Literal

OperationTimelineResponseState = Literal[
    "accepted", "cancelled", "failed", "requires_reconciliation", "running", "succeeded"
]

OPERATION_TIMELINE_RESPONSE_STATE_VALUES: set[OperationTimelineResponseState] = {
    "accepted",
    "cancelled",
    "failed",
    "requires_reconciliation",
    "running",
    "succeeded",
}


def check_operation_timeline_response_state(value: str) -> OperationTimelineResponseState:
    if value in OPERATION_TIMELINE_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_TIMELINE_RESPONSE_STATE_VALUES!r}")
