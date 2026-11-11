from typing import Literal

StoredWorkflowLiveExpectationState = Literal["dead", "failed", "skipped", "succeeded"]

STORED_WORKFLOW_LIVE_EXPECTATION_STATE_VALUES: set[StoredWorkflowLiveExpectationState] = {
    "dead",
    "failed",
    "skipped",
    "succeeded",
}


def check_stored_workflow_live_expectation_state(value: str) -> StoredWorkflowLiveExpectationState:
    if value in STORED_WORKFLOW_LIVE_EXPECTATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_LIVE_EXPECTATION_STATE_VALUES!r}")
