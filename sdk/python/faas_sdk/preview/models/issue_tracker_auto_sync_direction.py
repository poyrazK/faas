from typing import Literal

IssueTrackerAutoSyncDirection = Literal["both", "inbound", "outbound"]

ISSUE_TRACKER_AUTO_SYNC_DIRECTION_VALUES: set[IssueTrackerAutoSyncDirection] = {
    "both",
    "inbound",
    "outbound",
}


def check_issue_tracker_auto_sync_direction(value: str) -> IssueTrackerAutoSyncDirection:
    if value in ISSUE_TRACKER_AUTO_SYNC_DIRECTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_TRACKER_AUTO_SYNC_DIRECTION_VALUES!r}")
