from typing import Literal

StoredWorkflowLiveSuiteObservationResult = Literal["failed", "not_started", "passed", "pending", "unavailable"]

STORED_WORKFLOW_LIVE_SUITE_OBSERVATION_RESULT_VALUES: set[StoredWorkflowLiveSuiteObservationResult] = {
    "failed",
    "not_started",
    "passed",
    "pending",
    "unavailable",
}


def check_stored_workflow_live_suite_observation_result(value: str) -> StoredWorkflowLiveSuiteObservationResult:
    if value in STORED_WORKFLOW_LIVE_SUITE_OBSERVATION_RESULT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_LIVE_SUITE_OBSERVATION_RESULT_VALUES!r}"
    )
