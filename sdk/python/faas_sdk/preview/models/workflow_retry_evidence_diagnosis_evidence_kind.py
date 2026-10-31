from typing import Literal

WorkflowRetryEvidenceDiagnosisEvidenceKind = Literal["child", "parent"]

WORKFLOW_RETRY_EVIDENCE_DIAGNOSIS_EVIDENCE_KIND_VALUES: set[WorkflowRetryEvidenceDiagnosisEvidenceKind] = {
    "child",
    "parent",
}


def check_workflow_retry_evidence_diagnosis_evidence_kind(value: str) -> WorkflowRetryEvidenceDiagnosisEvidenceKind:
    if value in WORKFLOW_RETRY_EVIDENCE_DIAGNOSIS_EVIDENCE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RETRY_EVIDENCE_DIAGNOSIS_EVIDENCE_KIND_VALUES!r}"
    )
