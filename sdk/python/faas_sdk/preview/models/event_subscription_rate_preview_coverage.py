from typing import Literal

EventSubscriptionRatePreviewCoverage = Literal["retained_captured_pending_application_recipients"]

EVENT_SUBSCRIPTION_RATE_PREVIEW_COVERAGE_VALUES: set[EventSubscriptionRatePreviewCoverage] = {
    "retained_captured_pending_application_recipients",
}


def check_event_subscription_rate_preview_coverage(value: str) -> EventSubscriptionRatePreviewCoverage:
    if value in EVENT_SUBSCRIPTION_RATE_PREVIEW_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_RATE_PREVIEW_COVERAGE_VALUES!r}")
