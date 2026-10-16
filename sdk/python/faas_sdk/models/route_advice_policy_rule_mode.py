from typing import Literal

RouteAdvicePolicyRuleMode = Literal["enforce", "log"]

ROUTE_ADVICE_POLICY_RULE_MODE_VALUES: set[RouteAdvicePolicyRuleMode] = {
    "enforce",
    "log",
}


def check_route_advice_policy_rule_mode(value: str) -> RouteAdvicePolicyRuleMode:
    if value in ROUTE_ADVICE_POLICY_RULE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_ADVICE_POLICY_RULE_MODE_VALUES!r}")
