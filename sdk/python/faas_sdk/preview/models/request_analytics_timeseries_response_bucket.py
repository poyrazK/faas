from typing import Literal

RequestAnalyticsTimeseriesResponseBucket = Literal["1h"]

REQUEST_ANALYTICS_TIMESERIES_RESPONSE_BUCKET_VALUES: set[RequestAnalyticsTimeseriesResponseBucket] = {
    "1h",
}


def check_request_analytics_timeseries_response_bucket(value: str) -> RequestAnalyticsTimeseriesResponseBucket:
    if value in REQUEST_ANALYTICS_TIMESERIES_RESPONSE_BUCKET_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_TIMESERIES_RESPONSE_BUCKET_VALUES!r}"
    )
