from typing import Literal

RequestAnalyticsResponseSampledMeasures = Literal["included", "omitted"]

REQUEST_ANALYTICS_RESPONSE_SAMPLED_MEASURES_VALUES: set[RequestAnalyticsResponseSampledMeasures] = {
    "included",
    "omitted",
}


def check_request_analytics_response_sampled_measures(value: str) -> RequestAnalyticsResponseSampledMeasures:
    if value in REQUEST_ANALYTICS_RESPONSE_SAMPLED_MEASURES_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_RESPONSE_SAMPLED_MEASURES_VALUES!r}"
    )
