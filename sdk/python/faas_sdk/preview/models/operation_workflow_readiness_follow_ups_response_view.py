from typing import Literal

OperationWorkflowReadinessFollowUpsResponseView = Literal["next_actions"]

OPERATION_WORKFLOW_READINESS_FOLLOW_UPS_RESPONSE_VIEW_VALUES: set[OperationWorkflowReadinessFollowUpsResponseView] = {
    "next_actions",
}


def check_operation_workflow_readiness_follow_ups_response_view(
    value: str,
) -> OperationWorkflowReadinessFollowUpsResponseView:
    if value in OPERATION_WORKFLOW_READINESS_FOLLOW_UPS_RESPONSE_VIEW_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_FOLLOW_UPS_RESPONSE_VIEW_VALUES!r}"
    )
