from typing import Literal

RouteAdvicePolicyRuleKind = Literal["async", "cache", "throttle"]

ROUTE_ADVICE_POLICY_RULE_KIND_VALUES: set[RouteAdvicePolicyRuleKind] = {
    "async",
    "cache",
    "throttle",
}


def check_route_advice_policy_rule_kind(value: str) -> RouteAdvicePolicyRuleKind:
    if value in ROUTE_ADVICE_POLICY_RULE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_ADVICE_POLICY_RULE_KIND_VALUES!r}")
