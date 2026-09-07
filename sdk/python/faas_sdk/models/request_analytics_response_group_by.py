from typing import Literal

RequestAnalyticsResponseGroupBy = Literal["country", "referrer_host", "route", "status", "ua_family"]

REQUEST_ANALYTICS_RESPONSE_GROUP_BY_VALUES: set[RequestAnalyticsResponseGroupBy] = {
    "country",
    "referrer_host",
    "route",
    "status",
    "ua_family",
}


def check_request_analytics_response_group_by(value: str) -> RequestAnalyticsResponseGroupBy:
    if value in REQUEST_ANALYTICS_RESPONSE_GROUP_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_RESPONSE_GROUP_BY_VALUES!r}")
