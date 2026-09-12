from typing import Literal

CreateEdgeRuleRequestKind = Literal[
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

CREATE_EDGE_RULE_REQUEST_KIND_VALUES: set[CreateEdgeRuleRequestKind] = {
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


def check_create_edge_rule_request_kind(value: str) -> CreateEdgeRuleRequestKind:
    if value in CREATE_EDGE_RULE_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_EDGE_RULE_REQUEST_KIND_VALUES!r}")
