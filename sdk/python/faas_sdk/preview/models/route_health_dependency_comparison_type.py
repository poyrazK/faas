from typing import Literal

RouteHealthDependencyComparisonType = Literal[
    "app_dependency", "application", "guest_transport", "managed_binding", "outbound_integration", "platform_internal"
]

ROUTE_HEALTH_DEPENDENCY_COMPARISON_TYPE_VALUES: set[RouteHealthDependencyComparisonType] = {
    "app_dependency",
    "application",
    "guest_transport",
    "managed_binding",
    "outbound_integration",
    "platform_internal",
}


def check_route_health_dependency_comparison_type(value: str) -> RouteHealthDependencyComparisonType:
    if value in ROUTE_HEALTH_DEPENDENCY_COMPARISON_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_DEPENDENCY_COMPARISON_TYPE_VALUES!r}")
