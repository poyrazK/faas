from typing import Literal

OperationWorkflowPlaybookCompletionOutcomeAlignment = Literal["matches", "mismatch", "not_applicable", "unavailable"]

OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_OUTCOME_ALIGNMENT_VALUES: set[
    OperationWorkflowPlaybookCompletionOutcomeAlignment
] = {
    "matches",
    "mismatch",
    "not_applicable",
    "unavailable",
}


def check_operation_workflow_playbook_completion_outcome_alignment(
    value: str,
) -> OperationWorkflowPlaybookCompletionOutcomeAlignment:
    if value in OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_OUTCOME_ALIGNMENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_COMPLETION_OUTCOME_ALIGNMENT_VALUES!r}"
    )
