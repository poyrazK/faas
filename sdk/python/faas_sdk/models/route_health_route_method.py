from typing import Literal

RouteHealthRouteMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

ROUTE_HEALTH_ROUTE_METHOD_VALUES: set[RouteHealthRouteMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_route_health_route_method(value: str) -> RouteHealthRouteMethod:
    if value in ROUTE_HEALTH_ROUTE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_ROUTE_METHOD_VALUES!r}")
