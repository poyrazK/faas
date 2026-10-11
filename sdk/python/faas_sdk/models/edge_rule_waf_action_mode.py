from typing import Literal

EdgeRuleWAFActionMode = Literal["block", "observe", "warn"]

EDGE_RULE_WAF_ACTION_MODE_VALUES: set[EdgeRuleWAFActionMode] = {
    "block",
    "observe",
    "warn",
}


def check_edge_rule_waf_action_mode(value: str) -> EdgeRuleWAFActionMode:
    if value in EDGE_RULE_WAF_ACTION_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_WAF_ACTION_MODE_VALUES!r}")
