from typing import Literal

IssueState = Literal["ignored", "open", "resolved"]

ISSUE_STATE_VALUES: set[IssueState] = {
    "ignored",
    "open",
    "resolved",
}


def check_issue_state(value: str) -> IssueState:
    if value in ISSUE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_STATE_VALUES!r}")
