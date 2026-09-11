from typing import Literal

DebugGuestExecutionEvidenceOutcome = Literal["canceled", "handler_error", "http_error", "ok", "timeout"]

DEBUG_GUEST_EXECUTION_EVIDENCE_OUTCOME_VALUES: set[DebugGuestExecutionEvidenceOutcome] = {
    "canceled",
    "handler_error",
    "http_error",
    "ok",
    "timeout",
}


def check_debug_guest_execution_evidence_outcome(value: str) -> DebugGuestExecutionEvidenceOutcome:
    if value in DEBUG_GUEST_EXECUTION_EVIDENCE_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_GUEST_EXECUTION_EVIDENCE_OUTCOME_VALUES!r}")
