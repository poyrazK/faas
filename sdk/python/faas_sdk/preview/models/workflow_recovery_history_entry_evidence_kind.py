from typing import Literal

WorkflowRecoveryHistoryEntryEvidenceKind = Literal["child", "initial", "parent"]

WORKFLOW_RECOVERY_HISTORY_ENTRY_EVIDENCE_KIND_VALUES: set[WorkflowRecoveryHistoryEntryEvidenceKind] = {
    "child",
    "initial",
    "parent",
}


def check_workflow_recovery_history_entry_evidence_kind(value: str) -> WorkflowRecoveryHistoryEntryEvidenceKind:
    if value in WORKFLOW_RECOVERY_HISTORY_ENTRY_EVIDENCE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_HISTORY_ENTRY_EVIDENCE_KIND_VALUES!r}"
    )
