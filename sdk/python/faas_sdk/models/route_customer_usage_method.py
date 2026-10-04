from typing import Literal

RouteCustomerUsageMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

ROUTE_CUSTOMER_USAGE_METHOD_VALUES: set[RouteCustomerUsageMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_route_customer_usage_method(value: str) -> RouteCustomerUsageMethod:
    if value in ROUTE_CUSTOMER_USAGE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_CUSTOMER_USAGE_METHOD_VALUES!r}")
