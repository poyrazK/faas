from typing import Literal

UpdateIssueTrackerAutoSyncRequestExpectedIssueState = Literal["open", "resolved"]

UPDATE_ISSUE_TRACKER_AUTO_SYNC_REQUEST_EXPECTED_ISSUE_STATE_VALUES: set[
    UpdateIssueTrackerAutoSyncRequestExpectedIssueState
] = {
    "open",
    "resolved",
}


def check_update_issue_tracker_auto_sync_request_expected_issue_state(
    value: str,
) -> UpdateIssueTrackerAutoSyncRequestExpectedIssueState:
    if value in UPDATE_ISSUE_TRACKER_AUTO_SYNC_REQUEST_EXPECTED_ISSUE_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_ISSUE_TRACKER_AUTO_SYNC_REQUEST_EXPECTED_ISSUE_STATE_VALUES!r}"
    )
