from typing import Literal

UpdateNativeIssueTrackerStatusRequestDesiredState = Literal["open", "resolved"]

UPDATE_NATIVE_ISSUE_TRACKER_STATUS_REQUEST_DESIRED_STATE_VALUES: set[
    UpdateNativeIssueTrackerStatusRequestDesiredState
] = {
    "open",
    "resolved",
}


def check_update_native_issue_tracker_status_request_desired_state(
    value: str,
) -> UpdateNativeIssueTrackerStatusRequestDesiredState:
    if value in UPDATE_NATIVE_ISSUE_TRACKER_STATUS_REQUEST_DESIRED_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_NATIVE_ISSUE_TRACKER_STATUS_REQUEST_DESIRED_STATE_VALUES!r}"
    )
