from typing import Literal

OperationWorkflowReadinessSubscriptionRequestEventsItem = Literal[
    "newly_blocked", "newly_ready", "reassigned", "unavailable"
]

OPERATION_WORKFLOW_READINESS_SUBSCRIPTION_REQUEST_EVENTS_ITEM_VALUES: set[
    OperationWorkflowReadinessSubscriptionRequestEventsItem
] = {
    "newly_blocked",
    "newly_ready",
    "reassigned",
    "unavailable",
}


def check_operation_workflow_readiness_subscription_request_events_item(
    value: str,
) -> OperationWorkflowReadinessSubscriptionRequestEventsItem:
    if value in OPERATION_WORKFLOW_READINESS_SUBSCRIPTION_REQUEST_EVENTS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_SUBSCRIPTION_REQUEST_EVENTS_ITEM_VALUES!r}"
    )
