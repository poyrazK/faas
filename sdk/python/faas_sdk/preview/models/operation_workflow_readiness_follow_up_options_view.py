from typing import Literal

OperationWorkflowReadinessFollowUpOptionsView = Literal["follow_ups", "next_actions"]

OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_VIEW_VALUES: set[OperationWorkflowReadinessFollowUpOptionsView] = {
    "follow_ups",
    "next_actions",
}


def check_operation_workflow_readiness_follow_up_options_view(
    value: str,
) -> OperationWorkflowReadinessFollowUpOptionsView:
    if value in OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_VIEW_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_VIEW_VALUES!r}"
    )
