from typing import Literal

EdgeRuleValidateActionValidateMode = Literal["block", "observe", "warn"]

EDGE_RULE_VALIDATE_ACTION_VALIDATE_MODE_VALUES: set[EdgeRuleValidateActionValidateMode] = {
    "block",
    "observe",
    "warn",
}


def check_edge_rule_validate_action_validate_mode(value: str) -> EdgeRuleValidateActionValidateMode:
    if value in EDGE_RULE_VALIDATE_ACTION_VALIDATE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_VALIDATE_ACTION_VALIDATE_MODE_VALUES!r}")
