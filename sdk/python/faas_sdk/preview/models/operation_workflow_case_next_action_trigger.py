from typing import Literal

OperationWorkflowCaseNextActionTrigger = Literal["automatic", "human", "suggested"]

OPERATION_WORKFLOW_CASE_NEXT_ACTION_TRIGGER_VALUES: set[OperationWorkflowCaseNextActionTrigger] = {
    "automatic",
    "human",
    "suggested",
}


def check_operation_workflow_case_next_action_trigger(value: str) -> OperationWorkflowCaseNextActionTrigger:
    if value in OPERATION_WORKFLOW_CASE_NEXT_ACTION_TRIGGER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CASE_NEXT_ACTION_TRIGGER_VALUES!r}"
    )
