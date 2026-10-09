from typing import Literal

ListEdgeRuleEventsOutcome = Literal["logged", "matched"]

LIST_EDGE_RULE_EVENTS_OUTCOME_VALUES: set[ListEdgeRuleEventsOutcome] = {
    "logged",
    "matched",
}


def check_list_edge_rule_events_outcome(value: str) -> ListEdgeRuleEventsOutcome:
    if value in LIST_EDGE_RULE_EVENTS_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_EDGE_RULE_EVENTS_OUTCOME_VALUES!r}")
