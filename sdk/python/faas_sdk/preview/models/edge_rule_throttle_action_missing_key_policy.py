from typing import Literal

EdgeRuleThrottleActionMissingKeyPolicy = Literal["reject", "shared"]

EDGE_RULE_THROTTLE_ACTION_MISSING_KEY_POLICY_VALUES: set[EdgeRuleThrottleActionMissingKeyPolicy] = {
    "reject",
    "shared",
}


def check_edge_rule_throttle_action_missing_key_policy(value: str) -> EdgeRuleThrottleActionMissingKeyPolicy:
    if value in EDGE_RULE_THROTTLE_ACTION_MISSING_KEY_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EDGE_RULE_THROTTLE_ACTION_MISSING_KEY_POLICY_VALUES!r}"
    )
