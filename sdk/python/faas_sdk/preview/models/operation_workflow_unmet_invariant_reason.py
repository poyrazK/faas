from typing import Literal

OperationWorkflowUnmetInvariantReason = Literal["failed", "mismatched", "missing", "unknown"]

OPERATION_WORKFLOW_UNMET_INVARIANT_REASON_VALUES: set[OperationWorkflowUnmetInvariantReason] = {
    "failed",
    "mismatched",
    "missing",
    "unknown",
}


def check_operation_workflow_unmet_invariant_reason(value: str) -> OperationWorkflowUnmetInvariantReason:
    if value in OPERATION_WORKFLOW_UNMET_INVARIANT_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_UNMET_INVARIANT_REASON_VALUES!r}")
