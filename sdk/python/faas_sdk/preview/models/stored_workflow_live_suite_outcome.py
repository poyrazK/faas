from typing import Literal

StoredWorkflowLiveSuiteOutcome = Literal["failed", "incomplete", "outdated", "passed", "pending", "unavailable"]

STORED_WORKFLOW_LIVE_SUITE_OUTCOME_VALUES: set[StoredWorkflowLiveSuiteOutcome] = {
    "failed",
    "incomplete",
    "outdated",
    "passed",
    "pending",
    "unavailable",
}


def check_stored_workflow_live_suite_outcome(value: str) -> StoredWorkflowLiveSuiteOutcome:
    if value in STORED_WORKFLOW_LIVE_SUITE_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_LIVE_SUITE_OUTCOME_VALUES!r}")
