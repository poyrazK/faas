from typing import Literal

WorkflowRecoveryOverviewAlertDeliveryStatus = Literal[
    "delivered", "failed", "no_receivers", "pending", "relay_pending", "unknown"
]

WORKFLOW_RECOVERY_OVERVIEW_ALERT_DELIVERY_STATUS_VALUES: set[WorkflowRecoveryOverviewAlertDeliveryStatus] = {
    "delivered",
    "failed",
    "no_receivers",
    "pending",
    "relay_pending",
    "unknown",
}


def check_workflow_recovery_overview_alert_delivery_status(value: str) -> WorkflowRecoveryOverviewAlertDeliveryStatus:
    if value in WORKFLOW_RECOVERY_OVERVIEW_ALERT_DELIVERY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_OVERVIEW_ALERT_DELIVERY_STATUS_VALUES!r}"
    )
