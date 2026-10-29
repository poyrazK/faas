from typing import Literal

IssueEventSymbolicationStrategy = Literal["deployment-source-maps-v1"]

ISSUE_EVENT_SYMBOLICATION_STRATEGY_VALUES: set[IssueEventSymbolicationStrategy] = {
    "deployment-source-maps-v1",
}


def check_issue_event_symbolication_strategy(value: str) -> IssueEventSymbolicationStrategy:
    if value in ISSUE_EVENT_SYMBOLICATION_STRATEGY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_EVENT_SYMBOLICATION_STRATEGY_VALUES!r}")
