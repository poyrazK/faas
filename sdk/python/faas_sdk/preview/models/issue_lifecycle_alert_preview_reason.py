from typing import Literal

IssueLifecycleAlertPreviewReason = Literal["cooldown", "eligible", "environment_excluded"]

ISSUE_LIFECYCLE_ALERT_PREVIEW_REASON_VALUES: set[IssueLifecycleAlertPreviewReason] = {
    "cooldown",
    "eligible",
    "environment_excluded",
}


def check_issue_lifecycle_alert_preview_reason(value: str) -> IssueLifecycleAlertPreviewReason:
    if value in ISSUE_LIFECYCLE_ALERT_PREVIEW_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_LIFECYCLE_ALERT_PREVIEW_REASON_VALUES!r}")
