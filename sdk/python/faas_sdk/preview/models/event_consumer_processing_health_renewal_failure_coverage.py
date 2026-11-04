from typing import Literal

EventConsumerProcessingHealthRenewalFailureCoverage = Literal["not_recorded"]

EVENT_CONSUMER_PROCESSING_HEALTH_RENEWAL_FAILURE_COVERAGE_VALUES: set[
    EventConsumerProcessingHealthRenewalFailureCoverage
] = {
    "not_recorded",
}


def check_event_consumer_processing_health_renewal_failure_coverage(
    value: str,
) -> EventConsumerProcessingHealthRenewalFailureCoverage:
    if value in EVENT_CONSUMER_PROCESSING_HEALTH_RENEWAL_FAILURE_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_CONSUMER_PROCESSING_HEALTH_RENEWAL_FAILURE_COVERAGE_VALUES!r}"
    )
