from typing import Literal

IssueTrackerAutoSyncPendingState = Literal["open", "resolved"]

ISSUE_TRACKER_AUTO_SYNC_PENDING_STATE_VALUES: set[IssueTrackerAutoSyncPendingState] = {
    "open",
    "resolved",
}


def check_issue_tracker_auto_sync_pending_state(value: str) -> IssueTrackerAutoSyncPendingState:
    if value in ISSUE_TRACKER_AUTO_SYNC_PENDING_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_TRACKER_AUTO_SYNC_PENDING_STATE_VALUES!r}")
