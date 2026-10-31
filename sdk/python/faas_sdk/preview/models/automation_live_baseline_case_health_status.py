from typing import Literal

AutomationLiveBaselineCaseHealthStatus = Literal["failed", "not_started", "passed", "pending", "unavailable"]

AUTOMATION_LIVE_BASELINE_CASE_HEALTH_STATUS_VALUES: set[AutomationLiveBaselineCaseHealthStatus] = {
    "failed",
    "not_started",
    "passed",
    "pending",
    "unavailable",
}


def check_automation_live_baseline_case_health_status(value: str) -> AutomationLiveBaselineCaseHealthStatus:
    if value in AUTOMATION_LIVE_BASELINE_CASE_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {AUTOMATION_LIVE_BASELINE_CASE_HEALTH_STATUS_VALUES!r}"
    )
