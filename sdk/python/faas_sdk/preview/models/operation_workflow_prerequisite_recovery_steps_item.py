from typing import Literal

OperationWorkflowPrerequisiteRecoveryStepsItem = Literal["perform_operation", "publish_milestone", "recheck_readiness"]

OPERATION_WORKFLOW_PREREQUISITE_RECOVERY_STEPS_ITEM_VALUES: set[OperationWorkflowPrerequisiteRecoveryStepsItem] = {
    "perform_operation",
    "publish_milestone",
    "recheck_readiness",
}


def check_operation_workflow_prerequisite_recovery_steps_item(
    value: str,
) -> OperationWorkflowPrerequisiteRecoveryStepsItem:
    if value in OPERATION_WORKFLOW_PREREQUISITE_RECOVERY_STEPS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PREREQUISITE_RECOVERY_STEPS_ITEM_VALUES!r}"
    )
