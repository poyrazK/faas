from typing import Literal

RequestAnalyticsTimeseriesResponseGroupBy = Literal["country", "referrer_host", "route", "status", "ua_family"]

REQUEST_ANALYTICS_TIMESERIES_RESPONSE_GROUP_BY_VALUES: set[RequestAnalyticsTimeseriesResponseGroupBy] = {
    "country",
    "referrer_host",
    "route",
    "status",
    "ua_family",
}


def check_request_analytics_timeseries_response_group_by(value: str) -> RequestAnalyticsTimeseriesResponseGroupBy:
    if value in REQUEST_ANALYTICS_TIMESERIES_RESPONSE_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_TIMESERIES_RESPONSE_GROUP_BY_VALUES!r}"
    )
