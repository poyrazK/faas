from typing import Literal

OperationWorkflowReadinessCompletionRequestPlaybookResolution = Literal[
    "alternative_accepted", "expected_outcome_achieved", "follow_up_canceled"
]

OPERATION_WORKFLOW_READINESS_COMPLETION_REQUEST_PLAYBOOK_RESOLUTION_VALUES: set[
    OperationWorkflowReadinessCompletionRequestPlaybookResolution
] = {
    "alternative_accepted",
    "expected_outcome_achieved",
    "follow_up_canceled",
}


def check_operation_workflow_readiness_completion_request_playbook_resolution(
    value: str,
) -> OperationWorkflowReadinessCompletionRequestPlaybookResolution:
    if value in OPERATION_WORKFLOW_READINESS_COMPLETION_REQUEST_PLAYBOOK_RESOLUTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_COMPLETION_REQUEST_PLAYBOOK_RESOLUTION_VALUES!r}"
    )
