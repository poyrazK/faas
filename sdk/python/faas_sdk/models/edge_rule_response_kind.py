from typing import Literal

EdgeRuleResponseKind = Literal[
    "budget",
    "cache",
    "cors",
    "geo",
    "headers",
    "ip",
    "jwt",
    "limit",
    "maintenance",
    "redirect",
    "respond",
    "rewrite",
    "route",
    "throttle",
    "validate",
]

EDGE_RULE_RESPONSE_KIND_VALUES: set[EdgeRuleResponseKind] = {
    "budget",
    "cache",
    "cors",
    "geo",
    "headers",
    "ip",
    "jwt",
    "limit",
    "maintenance",
    "redirect",
    "respond",
    "rewrite",
    "route",
    "throttle",
    "validate",
}


def check_edge_rule_response_kind(value: str) -> EdgeRuleResponseKind:
    if value in EDGE_RULE_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_RESPONSE_KIND_VALUES!r}")
