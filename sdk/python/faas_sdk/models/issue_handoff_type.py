from typing import Literal

IssueHandoffType = Literal["issue.handoff"]

ISSUE_HANDOFF_TYPE_VALUES: set[IssueHandoffType] = {
    "issue.handoff",
}


def check_issue_handoff_type(value: str) -> IssueHandoffType:
    if value in ISSUE_HANDOFF_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_HANDOFF_TYPE_VALUES!r}")
