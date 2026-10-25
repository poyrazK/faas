from typing import Literal

OperationWorkflowReconciliationPayloadKind = Literal["gregale.workflow-reconciliation.v1"]

OPERATION_WORKFLOW_RECONCILIATION_PAYLOAD_KIND_VALUES: set[OperationWorkflowReconciliationPayloadKind] = {
    "gregale.workflow-reconciliation.v1",
}


def check_operation_workflow_reconciliation_payload_kind(value: str) -> OperationWorkflowReconciliationPayloadKind:
    if value in OPERATION_WORKFLOW_RECONCILIATION_PAYLOAD_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_RECONCILIATION_PAYLOAD_KIND_VALUES!r}"
    )
