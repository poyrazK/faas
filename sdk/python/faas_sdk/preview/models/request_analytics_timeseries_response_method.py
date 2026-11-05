from typing import Literal

RequestAnalyticsTimeseriesResponseMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

REQUEST_ANALYTICS_TIMESERIES_RESPONSE_METHOD_VALUES: set[RequestAnalyticsTimeseriesResponseMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_request_analytics_timeseries_response_method(value: str) -> RequestAnalyticsTimeseriesResponseMethod:
    if value in REQUEST_ANALYTICS_TIMESERIES_RESPONSE_METHOD_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_TIMESERIES_RESPONSE_METHOD_VALUES!r}"
    )
