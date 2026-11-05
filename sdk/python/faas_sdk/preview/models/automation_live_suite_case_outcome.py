from typing import Literal

AutomationLiveSuiteCaseOutcome = Literal["failed", "not_started", "outdated", "passed", "pending", "unavailable"]

AUTOMATION_LIVE_SUITE_CASE_OUTCOME_VALUES: set[AutomationLiveSuiteCaseOutcome] = {
    "failed",
    "not_started",
    "outdated",
    "passed",
    "pending",
    "unavailable",
}


def check_automation_live_suite_case_outcome(value: str) -> AutomationLiveSuiteCaseOutcome:
    if value in AUTOMATION_LIVE_SUITE_CASE_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_LIVE_SUITE_CASE_OUTCOME_VALUES!r}")
