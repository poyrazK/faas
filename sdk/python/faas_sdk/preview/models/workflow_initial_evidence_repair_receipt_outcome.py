from typing import Literal

WorkflowInitialEvidenceRepairReceiptOutcome = Literal["cancelled", "failed", "succeeded"]

WORKFLOW_INITIAL_EVIDENCE_REPAIR_RECEIPT_OUTCOME_VALUES: set[WorkflowInitialEvidenceRepairReceiptOutcome] = {
    "cancelled",
    "failed",
    "succeeded",
}


def check_workflow_initial_evidence_repair_receipt_outcome(value: str) -> WorkflowInitialEvidenceRepairReceiptOutcome:
    if value in WORKFLOW_INITIAL_EVIDENCE_REPAIR_RECEIPT_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_INITIAL_EVIDENCE_REPAIR_RECEIPT_OUTCOME_VALUES!r}"
    )
