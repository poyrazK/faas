from typing import Literal

OperationTimelineAttemptOutcome = Literal[
    "cancelled", "dead_letter", "failed", "retry", "running", "succeeded", "unknown"
]

OPERATION_TIMELINE_ATTEMPT_OUTCOME_VALUES: set[OperationTimelineAttemptOutcome] = {
    "cancelled",
    "dead_letter",
    "failed",
    "retry",
    "running",
    "succeeded",
    "unknown",
}


def check_operation_timeline_attempt_outcome(value: str) -> OperationTimelineAttemptOutcome:
    if value in OPERATION_TIMELINE_ATTEMPT_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_TIMELINE_ATTEMPT_OUTCOME_VALUES!r}")
