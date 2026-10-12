from typing import Literal

WorkflowRecoveryPolicyFailureStatusesItem = Literal["dead", "failed"]

WORKFLOW_RECOVERY_POLICY_FAILURE_STATUSES_ITEM_VALUES: set[WorkflowRecoveryPolicyFailureStatusesItem] = {
    "dead",
    "failed",
}


def check_workflow_recovery_policy_failure_statuses_item(value: str) -> WorkflowRecoveryPolicyFailureStatusesItem:
    if value in WORKFLOW_RECOVERY_POLICY_FAILURE_STATUSES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLICY_FAILURE_STATUSES_ITEM_VALUES!r}"
    )
