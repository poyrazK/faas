from typing import Literal

RoutePrioritiesResponseSource = Literal["configured", "none", "route_health"]

ROUTE_PRIORITIES_RESPONSE_SOURCE_VALUES: set[RoutePrioritiesResponseSource] = {
    "configured",
    "none",
    "route_health",
}


def check_route_priorities_response_source(value: str) -> RoutePrioritiesResponseSource:
    if value in ROUTE_PRIORITIES_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_PRIORITIES_RESPONSE_SOURCE_VALUES!r}")
