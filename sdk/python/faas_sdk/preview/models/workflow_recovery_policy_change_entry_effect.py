from typing import Literal

WorkflowRecoveryPolicyChangeEntryEffect = Literal["retain_slot", "would_suppress"]

WORKFLOW_RECOVERY_POLICY_CHANGE_ENTRY_EFFECT_VALUES: set[WorkflowRecoveryPolicyChangeEntryEffect] = {
    "retain_slot",
    "would_suppress",
}


def check_workflow_recovery_policy_change_entry_effect(value: str) -> WorkflowRecoveryPolicyChangeEntryEffect:
    if value in WORKFLOW_RECOVERY_POLICY_CHANGE_ENTRY_EFFECT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLICY_CHANGE_ENTRY_EFFECT_VALUES!r}"
    )
