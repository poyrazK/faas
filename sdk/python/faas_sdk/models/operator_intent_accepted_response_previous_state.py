from typing import Literal

OperatorIntentAcceptedResponsePreviousState = Literal["cold_booting", "running", "waking"]

OPERATOR_INTENT_ACCEPTED_RESPONSE_PREVIOUS_STATE_VALUES: set[OperatorIntentAcceptedResponsePreviousState] = {
    "cold_booting",
    "running",
    "waking",
}


def check_operator_intent_accepted_response_previous_state(value: str) -> OperatorIntentAcceptedResponsePreviousState:
    if value in OPERATOR_INTENT_ACCEPTED_RESPONSE_PREVIOUS_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATOR_INTENT_ACCEPTED_RESPONSE_PREVIOUS_STATE_VALUES!r}"
    )
