from typing import Literal

CreateEdgeRuleRequestValidateMode = Literal["block", "observe", "warn"]

CREATE_EDGE_RULE_REQUEST_VALIDATE_MODE_VALUES: set[CreateEdgeRuleRequestValidateMode] = {
    "block",
    "observe",
    "warn",
}


def check_create_edge_rule_request_validate_mode(value: str) -> CreateEdgeRuleRequestValidateMode:
    if value in CREATE_EDGE_RULE_REQUEST_VALIDATE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_EDGE_RULE_REQUEST_VALIDATE_MODE_VALUES!r}")
