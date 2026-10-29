from typing import Literal

AutomationLiveTestExpectedRunStatus = Literal["dead", "failed", "succeeded"]

AUTOMATION_LIVE_TEST_EXPECTED_RUN_STATUS_VALUES: set[AutomationLiveTestExpectedRunStatus] = {
    "dead",
    "failed",
    "succeeded",
}


def check_automation_live_test_expected_run_status(value: str) -> AutomationLiveTestExpectedRunStatus:
    if value in AUTOMATION_LIVE_TEST_EXPECTED_RUN_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_LIVE_TEST_EXPECTED_RUN_STATUS_VALUES!r}")
