from typing import Literal

IssueGroupingExplanationStrategy = Literal["cause-chain-v1", "failure-site-v1", "legacy"]

ISSUE_GROUPING_EXPLANATION_STRATEGY_VALUES: set[IssueGroupingExplanationStrategy] = {
    "cause-chain-v1",
    "failure-site-v1",
    "legacy",
}


def check_issue_grouping_explanation_strategy(value: str) -> IssueGroupingExplanationStrategy:
    if value in ISSUE_GROUPING_EXPLANATION_STRATEGY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_GROUPING_EXPLANATION_STRATEGY_VALUES!r}")
