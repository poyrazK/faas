from typing import Literal

UpdateEdgeRuleRequestMode = Literal["enforce", "log"]

UPDATE_EDGE_RULE_REQUEST_MODE_VALUES: set[UpdateEdgeRuleRequestMode] = {
    "enforce",
    "log",
}


def check_update_edge_rule_request_mode(value: str) -> UpdateEdgeRuleRequestMode:
    if value in UPDATE_EDGE_RULE_REQUEST_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_EDGE_RULE_REQUEST_MODE_VALUES!r}")
