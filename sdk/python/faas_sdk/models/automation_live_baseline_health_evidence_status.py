from typing import Literal

AutomationLiveBaselineHealthEvidenceStatus = Literal["failed", "unavailable", "usable"]

AUTOMATION_LIVE_BASELINE_HEALTH_EVIDENCE_STATUS_VALUES: set[AutomationLiveBaselineHealthEvidenceStatus] = {
    "failed",
    "unavailable",
    "usable",
}


def check_automation_live_baseline_health_evidence_status(value: str) -> AutomationLiveBaselineHealthEvidenceStatus:
    if value in AUTOMATION_LIVE_BASELINE_HEALTH_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {AUTOMATION_LIVE_BASELINE_HEALTH_EVIDENCE_STATUS_VALUES!r}"
    )
