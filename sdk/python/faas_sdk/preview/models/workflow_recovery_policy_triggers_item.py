from typing import Literal

WorkflowRecoveryPolicyTriggersItem = Literal["deadline_exceeded", "initial_failure", "saved_failure", "stalled"]

WORKFLOW_RECOVERY_POLICY_TRIGGERS_ITEM_VALUES: set[WorkflowRecoveryPolicyTriggersItem] = {
    "deadline_exceeded",
    "initial_failure",
    "saved_failure",
    "stalled",
}


def check_workflow_recovery_policy_triggers_item(value: str) -> WorkflowRecoveryPolicyTriggersItem:
    if value in WORKFLOW_RECOVERY_POLICY_TRIGGERS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLICY_TRIGGERS_ITEM_VALUES!r}")
