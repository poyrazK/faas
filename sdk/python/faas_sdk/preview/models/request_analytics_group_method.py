from typing import Literal

RequestAnalyticsGroupMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

REQUEST_ANALYTICS_GROUP_METHOD_VALUES: set[RequestAnalyticsGroupMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_request_analytics_group_method(value: str) -> RequestAnalyticsGroupMethod:
    if value in REQUEST_ANALYTICS_GROUP_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_GROUP_METHOD_VALUES!r}")
