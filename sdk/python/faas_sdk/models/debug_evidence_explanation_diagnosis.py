from typing import Literal

DebugEvidenceExplanationDiagnosis = Literal[
    "cold_start", "insufficient_evidence", "no_issue_observed", "performance_regression", "request_failure", "slow_path"
]

DEBUG_EVIDENCE_EXPLANATION_DIAGNOSIS_VALUES: set[DebugEvidenceExplanationDiagnosis] = {
    "cold_start",
    "insufficient_evidence",
    "no_issue_observed",
    "performance_regression",
    "request_failure",
    "slow_path",
}


def check_debug_evidence_explanation_diagnosis(value: str) -> DebugEvidenceExplanationDiagnosis:
    if value in DEBUG_EVIDENCE_EXPLANATION_DIAGNOSIS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_EVIDENCE_EXPLANATION_DIAGNOSIS_VALUES!r}")
