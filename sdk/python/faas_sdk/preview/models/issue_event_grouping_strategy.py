from typing import Literal

IssueEventGroupingStrategy = Literal["cause-chain-v1", "failure-site-v1", "legacy"]

ISSUE_EVENT_GROUPING_STRATEGY_VALUES: set[IssueEventGroupingStrategy] = {
    "cause-chain-v1",
    "failure-site-v1",
    "legacy",
}


def check_issue_event_grouping_strategy(value: str) -> IssueEventGroupingStrategy:
    if value in ISSUE_EVENT_GROUPING_STRATEGY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_EVENT_GROUPING_STRATEGY_VALUES!r}")
