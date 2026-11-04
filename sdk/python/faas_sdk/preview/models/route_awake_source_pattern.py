from typing import Literal

RouteAwakeSourcePattern = Literal["irregular", "periodic"]

ROUTE_AWAKE_SOURCE_PATTERN_VALUES: set[RouteAwakeSourcePattern] = {
    "irregular",
    "periodic",
}


def check_route_awake_source_pattern(value: str) -> RouteAwakeSourcePattern:
    if value in ROUTE_AWAKE_SOURCE_PATTERN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_AWAKE_SOURCE_PATTERN_VALUES!r}")
