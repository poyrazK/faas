from typing import Literal

RouteAdvicePolicyRuleSource = Literal["app_wide", "environment_policy", "host"]

ROUTE_ADVICE_POLICY_RULE_SOURCE_VALUES: set[RouteAdvicePolicyRuleSource] = {
    "app_wide",
    "environment_policy",
    "host",
}


def check_route_advice_policy_rule_source(value: str) -> RouteAdvicePolicyRuleSource:
    if value in ROUTE_ADVICE_POLICY_RULE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_ADVICE_POLICY_RULE_SOURCE_VALUES!r}")
