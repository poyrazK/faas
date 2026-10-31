from typing import Literal

OperatorIntentResponseStatus = Literal["cancelled", "failed", "outcome_unknown", "pending", "running", "succeeded"]

OPERATOR_INTENT_RESPONSE_STATUS_VALUES: set[OperatorIntentResponseStatus] = {
    "cancelled",
    "failed",
    "outcome_unknown",
    "pending",
    "running",
    "succeeded",
}


def check_operator_intent_response_status(value: str) -> OperatorIntentResponseStatus:
    if value in OPERATOR_INTENT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_INTENT_RESPONSE_STATUS_VALUES!r}")
