from typing import Literal

OperationWorkflowReadinessBusinessOutcomeStatus = Literal["achieved", "different_outcome", "pending", "unknown"]

OPERATION_WORKFLOW_READINESS_BUSINESS_OUTCOME_STATUS_VALUES: set[OperationWorkflowReadinessBusinessOutcomeStatus] = {
    "achieved",
    "different_outcome",
    "pending",
    "unknown",
}


def check_operation_workflow_readiness_business_outcome_status(
    value: str,
) -> OperationWorkflowReadinessBusinessOutcomeStatus:
    if value in OPERATION_WORKFLOW_READINESS_BUSINESS_OUTCOME_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_BUSINESS_OUTCOME_STATUS_VALUES!r}"
    )
