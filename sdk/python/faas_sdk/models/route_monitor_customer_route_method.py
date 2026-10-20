from typing import Literal

RouteMonitorCustomerRouteMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

ROUTE_MONITOR_CUSTOMER_ROUTE_METHOD_VALUES: set[RouteMonitorCustomerRouteMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_route_monitor_customer_route_method(value: str) -> RouteMonitorCustomerRouteMethod:
    if value in ROUTE_MONITOR_CUSTOMER_ROUTE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CUSTOMER_ROUTE_METHOD_VALUES!r}")
