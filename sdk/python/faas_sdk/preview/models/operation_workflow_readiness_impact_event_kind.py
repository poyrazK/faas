from typing import Literal

OperationWorkflowReadinessImpactEventKind = Literal["milestone", "outcome"]

OPERATION_WORKFLOW_READINESS_IMPACT_EVENT_KIND_VALUES: set[OperationWorkflowReadinessImpactEventKind] = {
    "milestone",
    "outcome",
}


def check_operation_workflow_readiness_impact_event_kind(value: str) -> OperationWorkflowReadinessImpactEventKind:
    if value in OPERATION_WORKFLOW_READINESS_IMPACT_EVENT_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_IMPACT_EVENT_KIND_VALUES!r}"
    )
