from typing import Literal

OperationWorkflowReadinessSubscriptionStatus = Literal["active", "retrying"]

OPERATION_WORKFLOW_READINESS_SUBSCRIPTION_STATUS_VALUES: set[OperationWorkflowReadinessSubscriptionStatus] = {
    "active",
    "retrying",
}


def check_operation_workflow_readiness_subscription_status(value: str) -> OperationWorkflowReadinessSubscriptionStatus:
    if value in OPERATION_WORKFLOW_READINESS_SUBSCRIPTION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_SUBSCRIPTION_STATUS_VALUES!r}"
    )
