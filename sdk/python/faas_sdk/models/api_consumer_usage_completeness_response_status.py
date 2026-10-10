from typing import Literal

APIConsumerUsageCompletenessResponseStatus = Literal["gaps_detected", "partial", "unverifiable", "verified"]

API_CONSUMER_USAGE_COMPLETENESS_RESPONSE_STATUS_VALUES: set[APIConsumerUsageCompletenessResponseStatus] = {
    "gaps_detected",
    "partial",
    "unverifiable",
    "verified",
}


def check_api_consumer_usage_completeness_response_status(value: str) -> APIConsumerUsageCompletenessResponseStatus:
    if value in API_CONSUMER_USAGE_COMPLETENESS_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_CONSUMER_USAGE_COMPLETENESS_RESPONSE_STATUS_VALUES!r}")
