from typing import Literal

RouteInsightFixAction = Literal["add_cache_override", "create_cache_rule", "disable_cache_rule", "set_cache_max_age"]

ROUTE_INSIGHT_FIX_ACTION_VALUES: set[RouteInsightFixAction] = {
    "add_cache_override",
    "create_cache_rule",
    "disable_cache_rule",
    "set_cache_max_age",
}


def check_route_insight_fix_action(value: str) -> RouteInsightFixAction:
    if value in ROUTE_INSIGHT_FIX_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_INSIGHT_FIX_ACTION_VALUES!r}")
