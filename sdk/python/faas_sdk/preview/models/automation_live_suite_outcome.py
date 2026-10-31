from typing import Literal

AutomationLiveSuiteOutcome = Literal["failed", "incomplete", "outdated", "passed", "pending", "unavailable"]

AUTOMATION_LIVE_SUITE_OUTCOME_VALUES: set[AutomationLiveSuiteOutcome] = {
    "failed",
    "incomplete",
    "outdated",
    "passed",
    "pending",
    "unavailable",
}


def check_automation_live_suite_outcome(value: str) -> AutomationLiveSuiteOutcome:
    if value in AUTOMATION_LIVE_SUITE_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_LIVE_SUITE_OUTCOME_VALUES!r}")
