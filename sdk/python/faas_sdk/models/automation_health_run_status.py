from typing import Literal

AutomationHealthRunStatus = Literal["awaiting_event", "dead", "failed", "pending", "running", "succeeded"]

AUTOMATION_HEALTH_RUN_STATUS_VALUES: set[AutomationHealthRunStatus] = {
    "awaiting_event",
    "dead",
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_automation_health_run_status(value: str) -> AutomationHealthRunStatus:
    if value in AUTOMATION_HEALTH_RUN_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_HEALTH_RUN_STATUS_VALUES!r}")
