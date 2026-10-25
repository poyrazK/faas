from typing import Literal

GetAppRequestAnalyticsGroupBy = Literal["consumer_id", "country", "referrer_host", "route", "status", "ua_family"]

GET_APP_REQUEST_ANALYTICS_GROUP_BY_VALUES: set[GetAppRequestAnalyticsGroupBy] = {
    "consumer_id",
    "country",
    "referrer_host",
    "route",
    "status",
    "ua_family",
}


def check_get_app_request_analytics_group_by(value: str) -> GetAppRequestAnalyticsGroupBy:
    if value in GET_APP_REQUEST_ANALYTICS_GROUP_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_REQUEST_ANALYTICS_GROUP_BY_VALUES!r}")
