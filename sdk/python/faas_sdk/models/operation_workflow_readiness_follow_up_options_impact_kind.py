from typing import Literal

OperationWorkflowReadinessFollowUpOptionsImpactKind = Literal["milestone", "outcome"]

OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_IMPACT_KIND_VALUES: set[
    OperationWorkflowReadinessFollowUpOptionsImpactKind
] = {
    "milestone",
    "outcome",
}


def check_operation_workflow_readiness_follow_up_options_impact_kind(
    value: str,
) -> OperationWorkflowReadinessFollowUpOptionsImpactKind:
    if value in OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_IMPACT_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_IMPACT_KIND_VALUES!r}"
    )
