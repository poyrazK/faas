from typing import Literal

OperationWorkflowReadinessEvidenceChangeKind = Literal[
    "added",
    "awaiting_verification",
    "changed",
    "cleared",
    "expired",
    "fulfilled",
    "invalidated",
    "observed",
    "outcome_mismatch",
    "reassigned",
    "required",
    "satisfied",
    "terminal",
    "unavailable",
    "unknown",
    "verified",
    "waiting",
]

OPERATION_WORKFLOW_READINESS_EVIDENCE_CHANGE_KIND_VALUES: set[OperationWorkflowReadinessEvidenceChangeKind] = {
    "added",
    "awaiting_verification",
    "changed",
    "cleared",
    "expired",
    "fulfilled",
    "invalidated",
    "observed",
    "outcome_mismatch",
    "reassigned",
    "required",
    "satisfied",
    "terminal",
    "unavailable",
    "unknown",
    "verified",
    "waiting",
}


def check_operation_workflow_readiness_evidence_change_kind(value: str) -> OperationWorkflowReadinessEvidenceChangeKind:
    if value in OPERATION_WORKFLOW_READINESS_EVIDENCE_CHANGE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_EVIDENCE_CHANGE_KIND_VALUES!r}"
    )
