from typing import Literal

OperationWorkflowActionPreviewResponseReason = Literal[
    "actions_available", "no_declared_transition", "state_unknown", "terminal"
]

OPERATION_WORKFLOW_ACTION_PREVIEW_RESPONSE_REASON_VALUES: set[OperationWorkflowActionPreviewResponseReason] = {
    "actions_available",
    "no_declared_transition",
    "state_unknown",
    "terminal",
}


def check_operation_workflow_action_preview_response_reason(value: str) -> OperationWorkflowActionPreviewResponseReason:
    if value in OPERATION_WORKFLOW_ACTION_PREVIEW_RESPONSE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_ACTION_PREVIEW_RESPONSE_REASON_VALUES!r}"
    )
