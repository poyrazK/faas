from typing import Literal

OperationWorkflowReadinessEvidenceStatus = Literal[
    "active",
    "awaiting_verification",
    "expired",
    "invalidated",
    "outcome_mismatch",
    "required",
    "satisfied",
    "terminal",
    "unknown",
    "verified",
    "waiting",
]

OPERATION_WORKFLOW_READINESS_EVIDENCE_STATUS_VALUES: set[OperationWorkflowReadinessEvidenceStatus] = {
    "active",
    "awaiting_verification",
    "expired",
    "invalidated",
    "outcome_mismatch",
    "required",
    "satisfied",
    "terminal",
    "unknown",
    "verified",
    "waiting",
}


def check_operation_workflow_readiness_evidence_status(value: str) -> OperationWorkflowReadinessEvidenceStatus:
    if value in OPERATION_WORKFLOW_READINESS_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_EVIDENCE_STATUS_VALUES!r}"
    )
