from typing import Literal

RequestAnalyticsRouteMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

REQUEST_ANALYTICS_ROUTE_METHOD_VALUES: set[RequestAnalyticsRouteMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_request_analytics_route_method(value: str) -> RequestAnalyticsRouteMethod:
    if value in REQUEST_ANALYTICS_ROUTE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_ROUTE_METHOD_VALUES!r}")
