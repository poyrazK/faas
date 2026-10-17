from typing import Literal

PreviewIssueLifecycleAlertRequestEvent = Literal["created", "regressed"]

PREVIEW_ISSUE_LIFECYCLE_ALERT_REQUEST_EVENT_VALUES: set[PreviewIssueLifecycleAlertRequestEvent] = {
    "created",
    "regressed",
}


def check_preview_issue_lifecycle_alert_request_event(value: str) -> PreviewIssueLifecycleAlertRequestEvent:
    if value in PREVIEW_ISSUE_LIFECYCLE_ALERT_REQUEST_EVENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PREVIEW_ISSUE_LIFECYCLE_ALERT_REQUEST_EVENT_VALUES!r}"
    )
