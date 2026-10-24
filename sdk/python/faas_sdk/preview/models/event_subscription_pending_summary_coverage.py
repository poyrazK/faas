from typing import Literal

EventSubscriptionPendingSummaryCoverage = Literal["all_retained_captured_pending_application_recipients"]

EVENT_SUBSCRIPTION_PENDING_SUMMARY_COVERAGE_VALUES: set[EventSubscriptionPendingSummaryCoverage] = {
    "all_retained_captured_pending_application_recipients",
}


def check_event_subscription_pending_summary_coverage(value: str) -> EventSubscriptionPendingSummaryCoverage:
    if value in EVENT_SUBSCRIPTION_PENDING_SUMMARY_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_PENDING_SUMMARY_COVERAGE_VALUES!r}"
    )
