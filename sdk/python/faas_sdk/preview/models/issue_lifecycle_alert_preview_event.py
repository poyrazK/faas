from typing import Literal

IssueLifecycleAlertPreviewEvent = Literal["created", "regressed"]

ISSUE_LIFECYCLE_ALERT_PREVIEW_EVENT_VALUES: set[IssueLifecycleAlertPreviewEvent] = {
    "created",
    "regressed",
}


def check_issue_lifecycle_alert_preview_event(value: str) -> IssueLifecycleAlertPreviewEvent:
    if value in ISSUE_LIFECYCLE_ALERT_PREVIEW_EVENT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_LIFECYCLE_ALERT_PREVIEW_EVENT_VALUES!r}")
