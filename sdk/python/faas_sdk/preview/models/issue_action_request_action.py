from typing import Literal

IssueActionRequestAction = Literal["assign", "ignore", "merge", "reopen", "resolve", "split"]

ISSUE_ACTION_REQUEST_ACTION_VALUES: set[IssueActionRequestAction] = {
    "assign",
    "ignore",
    "merge",
    "reopen",
    "resolve",
    "split",
}


def check_issue_action_request_action(value: str) -> IssueActionRequestAction:
    if value in ISSUE_ACTION_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_ACTION_REQUEST_ACTION_VALUES!r}")
