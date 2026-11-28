from typing import Literal

OperatorIntentAcceptedResponseKind = Literal["force_cold_boot", "force_park", "force_restart"]

OPERATOR_INTENT_ACCEPTED_RESPONSE_KIND_VALUES: set[OperatorIntentAcceptedResponseKind] = {
    "force_cold_boot",
    "force_park",
    "force_restart",
}


def check_operator_intent_accepted_response_kind(value: str) -> OperatorIntentAcceptedResponseKind:
    if value in OPERATOR_INTENT_ACCEPTED_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_INTENT_ACCEPTED_RESPONSE_KIND_VALUES!r}")
