from typing import Literal

OperationWorkflowUnmetEffectReason = Literal["failed", "mismatched", "missing", "pending"]

OPERATION_WORKFLOW_UNMET_EFFECT_REASON_VALUES: set[OperationWorkflowUnmetEffectReason] = {
    "failed",
    "mismatched",
    "missing",
    "pending",
}


def check_operation_workflow_unmet_effect_reason(value: str) -> OperationWorkflowUnmetEffectReason:
    if value in OPERATION_WORKFLOW_UNMET_EFFECT_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_UNMET_EFFECT_REASON_VALUES!r}")
