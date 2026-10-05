from typing import Literal

OperationResponseState = Literal["accepted", "cancelled", "failed", "requires_reconciliation", "running", "succeeded"]

OPERATION_RESPONSE_STATE_VALUES: set[OperationResponseState] = {
    "accepted",
    "cancelled",
    "failed",
    "requires_reconciliation",
    "running",
    "succeeded",
}


def check_operation_response_state(value: str) -> OperationResponseState:
    if value in OPERATION_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_RESPONSE_STATE_VALUES!r}")
