from typing import Literal

FlagLifecycleEvidence = Literal["available", "unavailable"]

FLAG_LIFECYCLE_EVIDENCE_VALUES: set[FlagLifecycleEvidence] = {
    "available",
    "unavailable",
}


def check_flag_lifecycle_evidence(value: str) -> FlagLifecycleEvidence:
    if value in FLAG_LIFECYCLE_EVIDENCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_LIFECYCLE_EVIDENCE_VALUES!r}")
