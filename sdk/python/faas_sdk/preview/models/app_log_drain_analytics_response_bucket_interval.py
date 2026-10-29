from typing import Literal

AppLogDrainAnalyticsResponseBucketInterval = Literal["1h"]

APP_LOG_DRAIN_ANALYTICS_RESPONSE_BUCKET_INTERVAL_VALUES: set[AppLogDrainAnalyticsResponseBucketInterval] = {
    "1h",
}


def check_app_log_drain_analytics_response_bucket_interval(value: str) -> AppLogDrainAnalyticsResponseBucketInterval:
    if value in APP_LOG_DRAIN_ANALYTICS_RESPONSE_BUCKET_INTERVAL_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_LOG_DRAIN_ANALYTICS_RESPONSE_BUCKET_INTERVAL_VALUES!r}"
    )
