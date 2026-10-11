from typing import Literal

CreateEdgeRuleRequestMode = Literal["enforce", "log"]

CREATE_EDGE_RULE_REQUEST_MODE_VALUES: set[CreateEdgeRuleRequestMode] = {
    "enforce",
    "log",
}


def check_create_edge_rule_request_mode(value: str) -> CreateEdgeRuleRequestMode:
    if value in CREATE_EDGE_RULE_REQUEST_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_EDGE_RULE_REQUEST_MODE_VALUES!r}")
