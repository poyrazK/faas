from typing import Literal

StoredWorkflowFailureTransitionState = Literal["paused", "resumed"]

STORED_WORKFLOW_FAILURE_TRANSITION_STATE_VALUES: set[StoredWorkflowFailureTransitionState] = {
    "paused",
    "resumed",
}


def check_stored_workflow_failure_transition_state(value: str) -> StoredWorkflowFailureTransitionState:
    if value in STORED_WORKFLOW_FAILURE_TRANSITION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_FAILURE_TRANSITION_STATE_VALUES!r}")
