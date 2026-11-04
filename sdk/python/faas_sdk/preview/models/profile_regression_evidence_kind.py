from typing import Literal

ProfileRegressionEvidenceKind = Literal["call_path", "function"]

PROFILE_REGRESSION_EVIDENCE_KIND_VALUES: set[ProfileRegressionEvidenceKind] = {
    "call_path",
    "function",
}


def check_profile_regression_evidence_kind(value: str) -> ProfileRegressionEvidenceKind:
    if value in PROFILE_REGRESSION_EVIDENCE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_REGRESSION_EVIDENCE_KIND_VALUES!r}")
