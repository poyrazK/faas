from typing import Literal

OperationWorkflowReadinessBusinessOutcomeEventPreviousStatus = Literal[
    "achieved", "different_outcome", "pending", "unknown"
]

OPERATION_WORKFLOW_READINESS_BUSINESS_OUTCOME_EVENT_PREVIOUS_STATUS_VALUES: set[
    OperationWorkflowReadinessBusinessOutcomeEventPreviousStatus
] = {
    "achieved",
    "different_outcome",
    "pending",
    "unknown",
}


def check_operation_workflow_readiness_business_outcome_event_previous_status(
    value: str,
) -> OperationWorkflowReadinessBusinessOutcomeEventPreviousStatus:
    if value in OPERATION_WORKFLOW_READINESS_BUSINESS_OUTCOME_EVENT_PREVIOUS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_BUSINESS_OUTCOME_EVENT_PREVIOUS_STATUS_VALUES!r}"
    )
