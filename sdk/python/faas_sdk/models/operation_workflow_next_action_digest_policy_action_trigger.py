from typing import Literal

OperationWorkflowNextActionDigestPolicyActionTrigger = Literal["automatic", "human", "suggested"]

OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_POLICY_ACTION_TRIGGER_VALUES: set[
    OperationWorkflowNextActionDigestPolicyActionTrigger
] = {
    "automatic",
    "human",
    "suggested",
}


def check_operation_workflow_next_action_digest_policy_action_trigger(
    value: str,
) -> OperationWorkflowNextActionDigestPolicyActionTrigger:
    if value in OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_POLICY_ACTION_TRIGGER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_POLICY_ACTION_TRIGGER_VALUES!r}"
    )
