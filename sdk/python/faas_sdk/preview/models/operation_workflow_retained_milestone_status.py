from typing import Literal

OperationWorkflowRetainedMilestoneStatus = Literal["awaiting_verification", "expired", "invalidated", "verified"]

OPERATION_WORKFLOW_RETAINED_MILESTONE_STATUS_VALUES: set[OperationWorkflowRetainedMilestoneStatus] = {
    "awaiting_verification",
    "expired",
    "invalidated",
    "verified",
}


def check_operation_workflow_retained_milestone_status(value: str) -> OperationWorkflowRetainedMilestoneStatus:
    if value in OPERATION_WORKFLOW_RETAINED_MILESTONE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_RETAINED_MILESTONE_STATUS_VALUES!r}"
    )
