from typing import Literal

RoutePriorityRuleMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

ROUTE_PRIORITY_RULE_METHOD_VALUES: set[RoutePriorityRuleMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_route_priority_rule_method(value: str) -> RoutePriorityRuleMethod:
    if value in ROUTE_PRIORITY_RULE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_PRIORITY_RULE_METHOD_VALUES!r}")
