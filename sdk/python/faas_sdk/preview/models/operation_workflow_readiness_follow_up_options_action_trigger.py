from typing import Literal

OperationWorkflowReadinessFollowUpOptionsActionTrigger = Literal["automatic", "human", "suggested"]

OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_ACTION_TRIGGER_VALUES: set[
    OperationWorkflowReadinessFollowUpOptionsActionTrigger
] = {
    "automatic",
    "human",
    "suggested",
}


def check_operation_workflow_readiness_follow_up_options_action_trigger(
    value: str,
) -> OperationWorkflowReadinessFollowUpOptionsActionTrigger:
    if value in OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_ACTION_TRIGGER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_ACTION_TRIGGER_VALUES!r}"
    )
