from typing import Literal

DebugEvidenceExplanationGeneratedBy = Literal["rules-v1"]

DEBUG_EVIDENCE_EXPLANATION_GENERATED_BY_VALUES: set[DebugEvidenceExplanationGeneratedBy] = {
    "rules-v1",
}


def check_debug_evidence_explanation_generated_by(value: str) -> DebugEvidenceExplanationGeneratedBy:
    if value in DEBUG_EVIDENCE_EXPLANATION_GENERATED_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_EVIDENCE_EXPLANATION_GENERATED_BY_VALUES!r}")
