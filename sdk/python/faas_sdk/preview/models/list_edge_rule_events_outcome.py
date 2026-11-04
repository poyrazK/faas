from typing import Literal

ListEdgeRuleEventsOutcome = Literal[
    "blocked", "boxed", "challenged", "detected", "failed", "logged", "matched", "rejected", "solved", "warned"
]

LIST_EDGE_RULE_EVENTS_OUTCOME_VALUES: set[ListEdgeRuleEventsOutcome] = {
    "blocked",
    "boxed",
    "challenged",
    "detected",
    "failed",
    "logged",
    "matched",
    "rejected",
    "solved",
    "warned",
}


def check_list_edge_rule_events_outcome(value: str) -> ListEdgeRuleEventsOutcome:
    if value in LIST_EDGE_RULE_EVENTS_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_EDGE_RULE_EVENTS_OUTCOME_VALUES!r}")
