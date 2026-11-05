from typing import Literal

OperationWorkflowReadinessImpactWatchKind = Literal["milestone", "outcome"]

OPERATION_WORKFLOW_READINESS_IMPACT_WATCH_KIND_VALUES: set[OperationWorkflowReadinessImpactWatchKind] = {
    "milestone",
    "outcome",
}


def check_operation_workflow_readiness_impact_watch_kind(value: str) -> OperationWorkflowReadinessImpactWatchKind:
    if value in OPERATION_WORKFLOW_READINESS_IMPACT_WATCH_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_IMPACT_WATCH_KIND_VALUES!r}"
    )
