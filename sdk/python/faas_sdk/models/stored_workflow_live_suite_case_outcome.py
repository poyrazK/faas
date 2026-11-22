from typing import Literal

StoredWorkflowLiveSuiteCaseOutcome = Literal["failed", "not_started", "outdated", "passed", "pending", "unavailable"]

STORED_WORKFLOW_LIVE_SUITE_CASE_OUTCOME_VALUES: set[StoredWorkflowLiveSuiteCaseOutcome] = {
    "failed",
    "not_started",
    "outdated",
    "passed",
    "pending",
    "unavailable",
}


def check_stored_workflow_live_suite_case_outcome(value: str) -> StoredWorkflowLiveSuiteCaseOutcome:
    if value in STORED_WORKFLOW_LIVE_SUITE_CASE_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_LIVE_SUITE_CASE_OUTCOME_VALUES!r}")
