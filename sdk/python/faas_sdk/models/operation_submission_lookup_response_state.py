from typing import Literal

OperationSubmissionLookupResponseState = Literal["accepted", "expired", "unresolved"]

OPERATION_SUBMISSION_LOOKUP_RESPONSE_STATE_VALUES: set[OperationSubmissionLookupResponseState] = {
    "accepted",
    "expired",
    "unresolved",
}


def check_operation_submission_lookup_response_state(value: str) -> OperationSubmissionLookupResponseState:
    if value in OPERATION_SUBMISSION_LOOKUP_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_SUBMISSION_LOOKUP_RESPONSE_STATE_VALUES!r}"
    )
