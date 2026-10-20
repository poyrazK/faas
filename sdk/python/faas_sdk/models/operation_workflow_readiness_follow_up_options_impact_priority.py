from typing import Literal

OperationWorkflowReadinessFollowUpOptionsImpactPriority = Literal["critical", "high", "normal"]

OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_IMPACT_PRIORITY_VALUES: set[
    OperationWorkflowReadinessFollowUpOptionsImpactPriority
] = {
    "critical",
    "high",
    "normal",
}


def check_operation_workflow_readiness_follow_up_options_impact_priority(
    value: str,
) -> OperationWorkflowReadinessFollowUpOptionsImpactPriority:
    if value in OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_IMPACT_PRIORITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_IMPACT_PRIORITY_VALUES!r}"
    )
