from typing import Literal

OperationWorkflowReadinessImpactEventReasonsItem = Literal[
    "critical_review_added", "review_became_overdue", "reviews_cleared"
]

OPERATION_WORKFLOW_READINESS_IMPACT_EVENT_REASONS_ITEM_VALUES: set[OperationWorkflowReadinessImpactEventReasonsItem] = {
    "critical_review_added",
    "review_became_overdue",
    "reviews_cleared",
}


def check_operation_workflow_readiness_impact_event_reasons_item(
    value: str,
) -> OperationWorkflowReadinessImpactEventReasonsItem:
    if value in OPERATION_WORKFLOW_READINESS_IMPACT_EVENT_REASONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_IMPACT_EVENT_REASONS_ITEM_VALUES!r}"
    )
