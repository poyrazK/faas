from typing import Literal

OperationWorkflowRevisionEvidenceKind = Literal["decision", "milestone", "verification_expected"]

OPERATION_WORKFLOW_REVISION_EVIDENCE_KIND_VALUES: set[OperationWorkflowRevisionEvidenceKind] = {
    "decision",
    "milestone",
    "verification_expected",
}


def check_operation_workflow_revision_evidence_kind(value: str) -> OperationWorkflowRevisionEvidenceKind:
    if value in OPERATION_WORKFLOW_REVISION_EVIDENCE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVISION_EVIDENCE_KIND_VALUES!r}")
