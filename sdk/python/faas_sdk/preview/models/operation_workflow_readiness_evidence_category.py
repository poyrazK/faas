from typing import Literal

OperationWorkflowReadinessEvidenceCategory = Literal["blockers", "prerequisites", "verification"]

OPERATION_WORKFLOW_READINESS_EVIDENCE_CATEGORY_VALUES: set[OperationWorkflowReadinessEvidenceCategory] = {
    "blockers",
    "prerequisites",
    "verification",
}


def check_operation_workflow_readiness_evidence_category(value: str) -> OperationWorkflowReadinessEvidenceCategory:
    if value in OPERATION_WORKFLOW_READINESS_EVIDENCE_CATEGORY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_EVIDENCE_CATEGORY_VALUES!r}"
    )
