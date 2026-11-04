from typing import Literal

UpdateIssueTrackerAutoSyncRequestDirection = Literal["both", "inbound", "outbound"]

UPDATE_ISSUE_TRACKER_AUTO_SYNC_REQUEST_DIRECTION_VALUES: set[UpdateIssueTrackerAutoSyncRequestDirection] = {
    "both",
    "inbound",
    "outbound",
}


def check_update_issue_tracker_auto_sync_request_direction(value: str) -> UpdateIssueTrackerAutoSyncRequestDirection:
    if value in UPDATE_ISSUE_TRACKER_AUTO_SYNC_REQUEST_DIRECTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_ISSUE_TRACKER_AUTO_SYNC_REQUEST_DIRECTION_VALUES!r}"
    )
