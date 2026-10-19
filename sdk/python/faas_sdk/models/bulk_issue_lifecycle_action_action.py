from typing import Literal

BulkIssueLifecycleActionAction = Literal["assign", "ignore", "reopen", "resolve"]

BULK_ISSUE_LIFECYCLE_ACTION_ACTION_VALUES: set[BulkIssueLifecycleActionAction] = {
    "assign",
    "ignore",
    "reopen",
    "resolve",
}


def check_bulk_issue_lifecycle_action_action(value: str) -> BulkIssueLifecycleActionAction:
    if value in BULK_ISSUE_LIFECYCLE_ACTION_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BULK_ISSUE_LIFECYCLE_ACTION_ACTION_VALUES!r}")
