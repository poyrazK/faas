from typing import Literal

OperationWorkflowReadinessBusinessOutcomeWatchLastNotifiedStatus = Literal[
    "achieved", "different_outcome", "pending", "unknown"
]

OPERATION_WORKFLOW_READINESS_BUSINESS_OUTCOME_WATCH_LAST_NOTIFIED_STATUS_VALUES: set[
    OperationWorkflowReadinessBusinessOutcomeWatchLastNotifiedStatus
] = {
    "achieved",
    "different_outcome",
    "pending",
    "unknown",
}


def check_operation_workflow_readiness_business_outcome_watch_last_notified_status(
    value: str,
) -> OperationWorkflowReadinessBusinessOutcomeWatchLastNotifiedStatus:
    if value in OPERATION_WORKFLOW_READINESS_BUSINESS_OUTCOME_WATCH_LAST_NOTIFIED_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_BUSINESS_OUTCOME_WATCH_LAST_NOTIFIED_STATUS_VALUES!r}"
    )
