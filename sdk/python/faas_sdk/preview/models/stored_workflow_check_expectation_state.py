from typing import Literal

StoredWorkflowCheckExpectationState = Literal[
    "blocked",
    "dead",
    "error",
    "expanded",
    "failed",
    "mocked",
    "resolved",
    "skipped",
    "timed_out",
    "would_execute",
    "would_retry",
    "would_wait",
]

STORED_WORKFLOW_CHECK_EXPECTATION_STATE_VALUES: set[StoredWorkflowCheckExpectationState] = {
    "blocked",
    "dead",
    "error",
    "expanded",
    "failed",
    "mocked",
    "resolved",
    "skipped",
    "timed_out",
    "would_execute",
    "would_retry",
    "would_wait",
}


def check_stored_workflow_check_expectation_state(value: str) -> StoredWorkflowCheckExpectationState:
    if value in STORED_WORKFLOW_CHECK_EXPECTATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_CHECK_EXPECTATION_STATE_VALUES!r}")
