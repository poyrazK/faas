from typing import Literal

RouteCustomerHealthRouteMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

ROUTE_CUSTOMER_HEALTH_ROUTE_METHOD_VALUES: set[RouteCustomerHealthRouteMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_route_customer_health_route_method(value: str) -> RouteCustomerHealthRouteMethod:
    if value in ROUTE_CUSTOMER_HEALTH_ROUTE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_CUSTOMER_HEALTH_ROUTE_METHOD_VALUES!r}")
