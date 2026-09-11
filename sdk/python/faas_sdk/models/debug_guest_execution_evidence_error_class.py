from typing import Literal

DebugGuestExecutionEvidenceErrorClass = Literal["canceled", "handler_exec", "handler_protocol", "http_5xx", "timeout"]

DEBUG_GUEST_EXECUTION_EVIDENCE_ERROR_CLASS_VALUES: set[DebugGuestExecutionEvidenceErrorClass] = {
    "canceled",
    "handler_exec",
    "handler_protocol",
    "http_5xx",
    "timeout",
}


def check_debug_guest_execution_evidence_error_class(value: str) -> DebugGuestExecutionEvidenceErrorClass:
    if value in DEBUG_GUEST_EXECUTION_EVIDENCE_ERROR_CLASS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DEBUG_GUEST_EXECUTION_EVIDENCE_ERROR_CLASS_VALUES!r}"
    )
