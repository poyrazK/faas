from typing import Literal

EventRecoveryRequestOutcome = Literal["dead_letter", "failed"]

EVENT_RECOVERY_REQUEST_OUTCOME_VALUES: set[EventRecoveryRequestOutcome] = {
    "dead_letter",
    "failed",
}


def check_event_recovery_request_outcome(value: str) -> EventRecoveryRequestOutcome:
    if value in EVENT_RECOVERY_REQUEST_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_REQUEST_OUTCOME_VALUES!r}")
