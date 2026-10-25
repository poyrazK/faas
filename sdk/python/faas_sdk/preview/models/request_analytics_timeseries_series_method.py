from typing import Literal

RequestAnalyticsTimeseriesSeriesMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

REQUEST_ANALYTICS_TIMESERIES_SERIES_METHOD_VALUES: set[RequestAnalyticsTimeseriesSeriesMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_request_analytics_timeseries_series_method(value: str) -> RequestAnalyticsTimeseriesSeriesMethod:
    if value in REQUEST_ANALYTICS_TIMESERIES_SERIES_METHOD_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_TIMESERIES_SERIES_METHOD_VALUES!r}"
    )
