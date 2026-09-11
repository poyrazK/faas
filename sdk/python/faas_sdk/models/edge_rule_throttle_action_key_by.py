from typing import Literal

EdgeRuleThrottleActionKeyBy = Literal["", "api_key", "consumer_id", "jwt_claim", "jwt_subject", "none"]

EDGE_RULE_THROTTLE_ACTION_KEY_BY_VALUES: set[EdgeRuleThrottleActionKeyBy] = {
    "",
    "api_key",
    "consumer_id",
    "jwt_claim",
    "jwt_subject",
    "none",
}


def check_edge_rule_throttle_action_key_by(value: str) -> EdgeRuleThrottleActionKeyBy:
    if value in EDGE_RULE_THROTTLE_ACTION_KEY_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_THROTTLE_ACTION_KEY_BY_VALUES!r}")
