from typing import Literal

DebugEvidenceExplanationStatus = Literal["regression_detected", "unobserved"]

DEBUG_EVIDENCE_EXPLANATION_STATUS_VALUES: set[DebugEvidenceExplanationStatus] = {
    "regression_detected",
    "unobserved",
}


def check_debug_evidence_explanation_status(value: str) -> DebugEvidenceExplanationStatus:
    if value in DEBUG_EVIDENCE_EXPLANATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_EVIDENCE_EXPLANATION_STATUS_VALUES!r}")
