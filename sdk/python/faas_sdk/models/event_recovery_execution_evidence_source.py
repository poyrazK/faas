from typing import Literal

EventRecoveryExecutionEvidenceSource = Literal["attempt_history", "invocation"]

EVENT_RECOVERY_EXECUTION_EVIDENCE_SOURCE_VALUES: set[EventRecoveryExecutionEvidenceSource] = {
    "attempt_history",
    "invocation",
}


def check_event_recovery_execution_evidence_source(value: str) -> EventRecoveryExecutionEvidenceSource:
    if value in EVENT_RECOVERY_EXECUTION_EVIDENCE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_EXECUTION_EVIDENCE_SOURCE_VALUES!r}")
