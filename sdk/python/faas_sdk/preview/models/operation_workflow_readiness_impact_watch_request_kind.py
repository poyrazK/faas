from typing import Literal

OperationWorkflowReadinessImpactWatchRequestKind = Literal["milestone", "outcome"]

OPERATION_WORKFLOW_READINESS_IMPACT_WATCH_REQUEST_KIND_VALUES: set[OperationWorkflowReadinessImpactWatchRequestKind] = {
    "milestone",
    "outcome",
}


def check_operation_workflow_readiness_impact_watch_request_kind(
    value: str,
) -> OperationWorkflowReadinessImpactWatchRequestKind:
    if value in OPERATION_WORKFLOW_READINESS_IMPACT_WATCH_REQUEST_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_IMPACT_WATCH_REQUEST_KIND_VALUES!r}"
    )
