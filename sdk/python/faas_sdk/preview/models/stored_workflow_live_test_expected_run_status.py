from typing import Literal

StoredWorkflowLiveTestExpectedRunStatus = Literal["dead", "failed", "succeeded"]

STORED_WORKFLOW_LIVE_TEST_EXPECTED_RUN_STATUS_VALUES: set[StoredWorkflowLiveTestExpectedRunStatus] = {
    "dead",
    "failed",
    "succeeded",
}


def check_stored_workflow_live_test_expected_run_status(value: str) -> StoredWorkflowLiveTestExpectedRunStatus:
    if value in STORED_WORKFLOW_LIVE_TEST_EXPECTED_RUN_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_LIVE_TEST_EXPECTED_RUN_STATUS_VALUES!r}"
    )
