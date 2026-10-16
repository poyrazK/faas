from typing import Literal

RouteInsightKind = Literal["cacheable", "kept_awake"]

ROUTE_INSIGHT_KIND_VALUES: set[RouteInsightKind] = {
    "cacheable",
    "kept_awake",
}


def check_route_insight_kind(value: str) -> RouteInsightKind:
    if value in ROUTE_INSIGHT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_INSIGHT_KIND_VALUES!r}")
