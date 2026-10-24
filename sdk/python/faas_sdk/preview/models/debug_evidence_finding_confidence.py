from typing import Literal

DebugEvidenceFindingConfidence = Literal["high", "low", "medium"]

DEBUG_EVIDENCE_FINDING_CONFIDENCE_VALUES: set[DebugEvidenceFindingConfidence] = {
    "high",
    "low",
    "medium",
}


def check_debug_evidence_finding_confidence(value: str) -> DebugEvidenceFindingConfidence:
    if value in DEBUG_EVIDENCE_FINDING_CONFIDENCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_EVIDENCE_FINDING_CONFIDENCE_VALUES!r}")
