from typing import Literal

AutomationLiveSuiteObservationResult = Literal["failed", "not_started", "passed", "pending", "unavailable"]

AUTOMATION_LIVE_SUITE_OBSERVATION_RESULT_VALUES: set[AutomationLiveSuiteObservationResult] = {
    "failed",
    "not_started",
    "passed",
    "pending",
    "unavailable",
}


def check_automation_live_suite_observation_result(value: str) -> AutomationLiveSuiteObservationResult:
    if value in AUTOMATION_LIVE_SUITE_OBSERVATION_RESULT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_LIVE_SUITE_OBSERVATION_RESULT_VALUES!r}")
