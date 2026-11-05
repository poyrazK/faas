from typing import Literal

IssueTrackerAutoSyncObservedState = Literal["open", "resolved"]

ISSUE_TRACKER_AUTO_SYNC_OBSERVED_STATE_VALUES: set[IssueTrackerAutoSyncObservedState] = {
    "open",
    "resolved",
}


def check_issue_tracker_auto_sync_observed_state(value: str) -> IssueTrackerAutoSyncObservedState:
    if value in ISSUE_TRACKER_AUTO_SYNC_OBSERVED_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_TRACKER_AUTO_SYNC_OBSERVED_STATE_VALUES!r}")
