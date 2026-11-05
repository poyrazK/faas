from typing import Literal

RouteMonitorRouteMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

ROUTE_MONITOR_ROUTE_METHOD_VALUES: set[RouteMonitorRouteMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_route_monitor_route_method(value: str) -> RouteMonitorRouteMethod:
    if value in ROUTE_MONITOR_ROUTE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_ROUTE_METHOD_VALUES!r}")
