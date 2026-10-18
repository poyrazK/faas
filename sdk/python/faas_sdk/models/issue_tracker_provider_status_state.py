from typing import Literal

IssueTrackerProviderStatusState = Literal["open", "resolved"]

ISSUE_TRACKER_PROVIDER_STATUS_STATE_VALUES: set[IssueTrackerProviderStatusState] = {
    "open",
    "resolved",
}


def check_issue_tracker_provider_status_state(value: str) -> IssueTrackerProviderStatusState:
    if value in ISSUE_TRACKER_PROVIDER_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_TRACKER_PROVIDER_STATUS_STATE_VALUES!r}")
