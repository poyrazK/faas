from typing import Literal

RouteHealthDependencyComparisonStatus = Literal["compared", "one_sided"]

ROUTE_HEALTH_DEPENDENCY_COMPARISON_STATUS_VALUES: set[RouteHealthDependencyComparisonStatus] = {
    "compared",
    "one_sided",
}


def check_route_health_dependency_comparison_status(value: str) -> RouteHealthDependencyComparisonStatus:
    if value in ROUTE_HEALTH_DEPENDENCY_COMPARISON_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_DEPENDENCY_COMPARISON_STATUS_VALUES!r}")
