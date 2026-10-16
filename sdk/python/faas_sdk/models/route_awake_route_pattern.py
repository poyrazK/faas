from typing import Literal

RouteAwakeRoutePattern = Literal["irregular", "periodic"]

ROUTE_AWAKE_ROUTE_PATTERN_VALUES: set[RouteAwakeRoutePattern] = {
    "irregular",
    "periodic",
}


def check_route_awake_route_pattern(value: str) -> RouteAwakeRoutePattern:
    if value in ROUTE_AWAKE_ROUTE_PATTERN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_AWAKE_ROUTE_PATTERN_VALUES!r}")
