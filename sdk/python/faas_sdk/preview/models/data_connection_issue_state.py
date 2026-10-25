from typing import Literal

DataConnectionIssueState = Literal["blocked", "passed", "unknown"]

DATA_CONNECTION_ISSUE_STATE_VALUES: set[DataConnectionIssueState] = {
    "blocked",
    "passed",
    "unknown",
}


def check_data_connection_issue_state(value: str) -> DataConnectionIssueState:
    if value in DATA_CONNECTION_ISSUE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_CONNECTION_ISSUE_STATE_VALUES!r}")
