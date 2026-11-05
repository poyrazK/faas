from typing import Literal

DebugEvidenceExplanationConfidence = Literal["high", "low", "medium"]

DEBUG_EVIDENCE_EXPLANATION_CONFIDENCE_VALUES: set[DebugEvidenceExplanationConfidence] = {
    "high",
    "low",
    "medium",
}


def check_debug_evidence_explanation_confidence(value: str) -> DebugEvidenceExplanationConfidence:
    if value in DEBUG_EVIDENCE_EXPLANATION_CONFIDENCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_EVIDENCE_EXPLANATION_CONFIDENCE_VALUES!r}")
