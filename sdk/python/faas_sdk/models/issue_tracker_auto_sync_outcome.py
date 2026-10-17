from typing import Literal

IssueTrackerAutoSyncOutcome = Literal["applied", "blocked", "conflict", "disabled", "error", "matched", "pending"]

ISSUE_TRACKER_AUTO_SYNC_OUTCOME_VALUES: set[IssueTrackerAutoSyncOutcome] = {
    "applied",
    "blocked",
    "conflict",
    "disabled",
    "error",
    "matched",
    "pending",
}


def check_issue_tracker_auto_sync_outcome(value: str) -> IssueTrackerAutoSyncOutcome:
    if value in ISSUE_TRACKER_AUTO_SYNC_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_TRACKER_AUTO_SYNC_OUTCOME_VALUES!r}")
