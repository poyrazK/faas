from typing import Literal

AutomationLiveBaselineHealthRenewalStatus = Literal["failed", "inconclusive", "passed"]

AUTOMATION_LIVE_BASELINE_HEALTH_RENEWAL_STATUS_VALUES: set[AutomationLiveBaselineHealthRenewalStatus] = {
    "failed",
    "inconclusive",
    "passed",
}


def check_automation_live_baseline_health_renewal_status(value: str) -> AutomationLiveBaselineHealthRenewalStatus:
    if value in AUTOMATION_LIVE_BASELINE_HEALTH_RENEWAL_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {AUTOMATION_LIVE_BASELINE_HEALTH_RENEWAL_STATUS_VALUES!r}"
    )
