from typing import Literal

OperationWorkflowPlaybookCompletionOutcomeResolution = Literal[
    "alternative_accepted", "expected_outcome_achieved", "follow_up_canceled"
]

OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_OUTCOME_RESOLUTION_VALUES: set[
    OperationWorkflowPlaybookCompletionOutcomeResolution
] = {
    "alternative_accepted",
    "expected_outcome_achieved",
    "follow_up_canceled",
}


def check_operation_workflow_playbook_completion_outcome_resolution(
    value: str,
) -> OperationWorkflowPlaybookCompletionOutcomeResolution:
    if value in OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_OUTCOME_RESOLUTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_OUTCOME_RESOLUTION_VALUES!r}"
    )
