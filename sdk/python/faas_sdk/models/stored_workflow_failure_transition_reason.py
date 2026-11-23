from typing import Literal

StoredWorkflowFailureTransitionReason = Literal["failure_threshold", "operator_resume"]

STORED_WORKFLOW_FAILURE_TRANSITION_REASON_VALUES: set[StoredWorkflowFailureTransitionReason] = {
    "failure_threshold",
    "operator_resume",
}


def check_stored_workflow_failure_transition_reason(value: str) -> StoredWorkflowFailureTransitionReason:
    if value in STORED_WORKFLOW_FAILURE_TRANSITION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_FAILURE_TRANSITION_REASON_VALUES!r}")
