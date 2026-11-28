from typing import Literal

EventSubscriptionDeadlinePageCoverage = Literal["retained_captured_pending_application_deadlines"]

EVENT_SUBSCRIPTION_DEADLINE_PAGE_COVERAGE_VALUES: set[EventSubscriptionDeadlinePageCoverage] = {
    "retained_captured_pending_application_deadlines",
}


def check_event_subscription_deadline_page_coverage(value: str) -> EventSubscriptionDeadlinePageCoverage:
    if value in EVENT_SUBSCRIPTION_DEADLINE_PAGE_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_DEADLINE_PAGE_COVERAGE_VALUES!r}")
