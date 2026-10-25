from typing import Literal

DebugGuestExecutionEvidenceRuntime = Literal["go124", "node22", "node24", "python312", "python313"]

DEBUG_GUEST_EXECUTION_EVIDENCE_RUNTIME_VALUES: set[DebugGuestExecutionEvidenceRuntime] = {
    "go124",
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_debug_guest_execution_evidence_runtime(value: str) -> DebugGuestExecutionEvidenceRuntime:
    if value in DEBUG_GUEST_EXECUTION_EVIDENCE_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_GUEST_EXECUTION_EVIDENCE_RUNTIME_VALUES!r}")
