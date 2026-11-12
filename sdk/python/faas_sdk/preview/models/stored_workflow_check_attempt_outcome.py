from typing import Literal

StoredWorkflowCheckAttemptOutcome = Literal["failure", "success", "timeout"]

STORED_WORKFLOW_CHECK_ATTEMPT_OUTCOME_VALUES: set[StoredWorkflowCheckAttemptOutcome] = {
    "failure",
    "success",
    "timeout",
}


def check_stored_workflow_check_attempt_outcome(value: str) -> StoredWorkflowCheckAttemptOutcome:
    if value in STORED_WORKFLOW_CHECK_ATTEMPT_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_CHECK_ATTEMPT_OUTCOME_VALUES!r}")
