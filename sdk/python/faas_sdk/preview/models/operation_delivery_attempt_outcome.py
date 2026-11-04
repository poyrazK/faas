from typing import Literal

OperationDeliveryAttemptOutcome = Literal["dead", "retrying", "succeeded"]

OPERATION_DELIVERY_ATTEMPT_OUTCOME_VALUES: set[OperationDeliveryAttemptOutcome] = {
    "dead",
    "retrying",
    "succeeded",
}


def check_operation_delivery_attempt_outcome(value: str) -> OperationDeliveryAttemptOutcome:
    if value in OPERATION_DELIVERY_ATTEMPT_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DELIVERY_ATTEMPT_OUTCOME_VALUES!r}")
