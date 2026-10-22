from typing import Literal

OperationWorkflowReadinessAssignmentChangeReason = Literal[
    "manual_assignment", "response_deadline_fallback", "step_assignment"
]

OPERATION_WORKFLOW_READINESS_ASSIGNMENT_CHANGE_REASON_VALUES: set[OperationWorkflowReadinessAssignmentChangeReason] = {
    "manual_assignment",
    "response_deadline_fallback",
    "step_assignment",
}


def check_operation_workflow_readiness_assignment_change_reason(
    value: str,
) -> OperationWorkflowReadinessAssignmentChangeReason:
    if value in OPERATION_WORKFLOW_READINESS_ASSIGNMENT_CHANGE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_ASSIGNMENT_CHANGE_REASON_VALUES!r}"
    )
