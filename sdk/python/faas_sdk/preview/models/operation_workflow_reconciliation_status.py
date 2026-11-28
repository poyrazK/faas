from typing import Literal

OperationWorkflowReconciliationStatus = Literal[
    "contract_version_mismatch", "in_sync", "report_ahead", "report_behind", "report_missing", "state_mismatch"
]

OPERATION_WORKFLOW_RECONCILIATION_STATUS_VALUES: set[OperationWorkflowReconciliationStatus] = {
    "contract_version_mismatch",
    "in_sync",
    "report_ahead",
    "report_behind",
    "report_missing",
    "state_mismatch",
}


def check_operation_workflow_reconciliation_status(value: str) -> OperationWorkflowReconciliationStatus:
    if value in OPERATION_WORKFLOW_RECONCILIATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_RECONCILIATION_STATUS_VALUES!r}")
