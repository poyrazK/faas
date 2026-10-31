from typing import Literal

EventSubscriptionDeadlineWindowCoverage = Literal["all_retained_captured_pending_application_deadline_window"]

EVENT_SUBSCRIPTION_DEADLINE_WINDOW_COVERAGE_VALUES: set[EventSubscriptionDeadlineWindowCoverage] = {
    "all_retained_captured_pending_application_deadline_window",
}


def check_event_subscription_deadline_window_coverage(value: str) -> EventSubscriptionDeadlineWindowCoverage:
    if value in EVENT_SUBSCRIPTION_DEADLINE_WINDOW_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_DEADLINE_WINDOW_COVERAGE_VALUES!r}"
    )
