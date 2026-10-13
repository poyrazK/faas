from typing import Literal

OperationWorkflowPlaybookStepReadinessStatus = Literal["ready", "waiting"]

OPERATION_WORKFLOW_PLAYBOOK_STEP_READINESS_STATUS_VALUES: set[OperationWorkflowPlaybookStepReadinessStatus] = {
    "ready",
    "waiting",
}


def check_operation_workflow_playbook_step_readiness_status(value: str) -> OperationWorkflowPlaybookStepReadinessStatus:
    if value in OPERATION_WORKFLOW_PLAYBOOK_STEP_READINESS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_STEP_READINESS_STATUS_VALUES!r}"
    )
