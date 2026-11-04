from typing import Literal

RoutePriorityRuleClass = Literal["bulk", "critical"]

ROUTE_PRIORITY_RULE_CLASS_VALUES: set[RoutePriorityRuleClass] = {
    "bulk",
    "critical",
}


def check_route_priority_rule_class(value: str) -> RoutePriorityRuleClass:
    if value in ROUTE_PRIORITY_RULE_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_PRIORITY_RULE_CLASS_VALUES!r}")
