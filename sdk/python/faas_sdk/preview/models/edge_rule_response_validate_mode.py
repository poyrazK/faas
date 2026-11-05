from typing import Literal

EdgeRuleResponseValidateMode = Literal["block", "observe", "warn"]

EDGE_RULE_RESPONSE_VALIDATE_MODE_VALUES: set[EdgeRuleResponseValidateMode] = {
    "block",
    "observe",
    "warn",
}


def check_edge_rule_response_validate_mode(value: str) -> EdgeRuleResponseValidateMode:
    if value in EDGE_RULE_RESPONSE_VALIDATE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_RESPONSE_VALIDATE_MODE_VALUES!r}")
