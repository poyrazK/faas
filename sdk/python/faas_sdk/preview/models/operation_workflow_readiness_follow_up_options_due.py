from typing import Literal

OperationWorkflowReadinessFollowUpOptionsDue = Literal["all", "overdue", "upcoming"]

OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_DUE_VALUES: set[OperationWorkflowReadinessFollowUpOptionsDue] = {
    "all",
    "overdue",
    "upcoming",
}


def check_operation_workflow_readiness_follow_up_options_due(
    value: str,
) -> OperationWorkflowReadinessFollowUpOptionsDue:
    if value in OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_DUE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_DUE_VALUES!r}"
    )
