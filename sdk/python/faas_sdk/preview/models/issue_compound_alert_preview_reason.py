from typing import Literal

IssueCompoundAlertPreviewReason = Literal[
    "cooldown", "customer_threshold", "disabled", "eligible", "environment_excluded", "event_threshold", "suppressed"
]

ISSUE_COMPOUND_ALERT_PREVIEW_REASON_VALUES: set[IssueCompoundAlertPreviewReason] = {
    "cooldown",
    "customer_threshold",
    "disabled",
    "eligible",
    "environment_excluded",
    "event_threshold",
    "suppressed",
}


def check_issue_compound_alert_preview_reason(value: str) -> IssueCompoundAlertPreviewReason:
    if value in ISSUE_COMPOUND_ALERT_PREVIEW_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_COMPOUND_ALERT_PREVIEW_REASON_VALUES!r}")
