from typing import Literal

EdgeRuleEventResponseOutcome = Literal[
    "blocked", "boxed", "challenged", "detected", "failed", "logged", "matched", "rejected", "solved", "warned"
]

EDGE_RULE_EVENT_RESPONSE_OUTCOME_VALUES: set[EdgeRuleEventResponseOutcome] = {
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


def check_edge_rule_event_response_outcome(value: str) -> EdgeRuleEventResponseOutcome:
    if value in EDGE_RULE_EVENT_RESPONSE_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_EVENT_RESPONSE_OUTCOME_VALUES!r}")
