from typing import Literal

EdgeRuleResponseMode = Literal["enforce", "log"]

EDGE_RULE_RESPONSE_MODE_VALUES: set[EdgeRuleResponseMode] = {
    "enforce",
    "log",
}


def check_edge_rule_response_mode(value: str) -> EdgeRuleResponseMode:
    if value in EDGE_RULE_RESPONSE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_RESPONSE_MODE_VALUES!r}")
