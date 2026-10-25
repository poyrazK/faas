from typing import Literal

OperationWorkflowReadinessImpactSummaryKind = Literal["milestone", "outcome"]

OPERATION_WORKFLOW_READINESS_IMPACT_SUMMARY_KIND_VALUES: set[OperationWorkflowReadinessImpactSummaryKind] = {
    "milestone",
    "outcome",
}


def check_operation_workflow_readiness_impact_summary_kind(value: str) -> OperationWorkflowReadinessImpactSummaryKind:
    if value in OPERATION_WORKFLOW_READINESS_IMPACT_SUMMARY_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_IMPACT_SUMMARY_KIND_VALUES!r}"
    )
