from typing import Literal

IssueTrackerAutoSyncSourceState = Literal["open", "resolved"]

ISSUE_TRACKER_AUTO_SYNC_SOURCE_STATE_VALUES: set[IssueTrackerAutoSyncSourceState] = {
    "open",
    "resolved",
}


def check_issue_tracker_auto_sync_source_state(value: str) -> IssueTrackerAutoSyncSourceState:
    if value in ISSUE_TRACKER_AUTO_SYNC_SOURCE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_TRACKER_AUTO_SYNC_SOURCE_STATE_VALUES!r}")
