from typing import Literal

UpdateEdgeRuleRequestValidateMode = Literal["block", "observe", "warn"]

UPDATE_EDGE_RULE_REQUEST_VALIDATE_MODE_VALUES: set[UpdateEdgeRuleRequestValidateMode] = {
    "block",
    "observe",
    "warn",
}


def check_update_edge_rule_request_validate_mode(value: str) -> UpdateEdgeRuleRequestValidateMode:
    if value in UPDATE_EDGE_RULE_REQUEST_VALIDATE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_EDGE_RULE_REQUEST_VALIDATE_MODE_VALUES!r}")
