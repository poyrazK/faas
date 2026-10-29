from typing import Literal

GetAppRequestAnalyticsTimeseriesMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

GET_APP_REQUEST_ANALYTICS_TIMESERIES_METHOD_VALUES: set[GetAppRequestAnalyticsTimeseriesMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_get_app_request_analytics_timeseries_method(value: str) -> GetAppRequestAnalyticsTimeseriesMethod:
    if value in GET_APP_REQUEST_ANALYTICS_TIMESERIES_METHOD_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GET_APP_REQUEST_ANALYTICS_TIMESERIES_METHOD_VALUES!r}"
    )
