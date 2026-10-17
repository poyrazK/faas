from typing import Literal

IssueTrackerAutoSyncProviderState = Literal["open", "resolved"]

ISSUE_TRACKER_AUTO_SYNC_PROVIDER_STATE_VALUES: set[IssueTrackerAutoSyncProviderState] = {
    "open",
    "resolved",
}


def check_issue_tracker_auto_sync_provider_state(value: str) -> IssueTrackerAutoSyncProviderState:
    if value in ISSUE_TRACKER_AUTO_SYNC_PROVIDER_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_TRACKER_AUTO_SYNC_PROVIDER_STATE_VALUES!r}")
