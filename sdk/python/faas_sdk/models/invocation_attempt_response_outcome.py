from typing import Literal

InvocationAttemptResponseOutcome = Literal[
    "cancelled", "dead_letter", "failed", "retry", "running", "succeeded", "unknown"
]

INVOCATION_ATTEMPT_RESPONSE_OUTCOME_VALUES: set[InvocationAttemptResponseOutcome] = {
    "cancelled",
    "dead_letter",
    "failed",
    "retry",
    "running",
    "succeeded",
    "unknown",
}


def check_invocation_attempt_response_outcome(value: str) -> InvocationAttemptResponseOutcome:
    if value in INVOCATION_ATTEMPT_RESPONSE_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INVOCATION_ATTEMPT_RESPONSE_OUTCOME_VALUES!r}")
