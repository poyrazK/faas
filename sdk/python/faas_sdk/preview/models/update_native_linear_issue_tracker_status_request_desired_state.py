from typing import Literal

UpdateNativeLinearIssueTrackerStatusRequestDesiredState = Literal["open", "resolved"]

UPDATE_NATIVE_LINEAR_ISSUE_TRACKER_STATUS_REQUEST_DESIRED_STATE_VALUES: set[
    UpdateNativeLinearIssueTrackerStatusRequestDesiredState
] = {
    "open",
    "resolved",
}


def check_update_native_linear_issue_tracker_status_request_desired_state(
    value: str,
) -> UpdateNativeLinearIssueTrackerStatusRequestDesiredState:
    if value in UPDATE_NATIVE_LINEAR_ISSUE_TRACKER_STATUS_REQUEST_DESIRED_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_NATIVE_LINEAR_ISSUE_TRACKER_STATUS_REQUEST_DESIRED_STATE_VALUES!r}"
    )
